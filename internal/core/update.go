package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/notify"
	"github.com/MatHoyer/kipitiny/internal/registry"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	updateEvery      = 6 * time.Hour
	updaterName      = "kipitiny-updater"
	updaterComponent = "updater"

	// StopTimeout is how long the manager's container gets to stop: enough
	// to drain running operations. The updater and the compose file's
	// stop_grace_period use it.
	StopTimeout = DrainTimeout + shutdownCancelGrace + 15*time.Second
	// UpdateHealthTimeout is how long a new version gets to become healthy
	// before the updater restores the previous one.
	UpdateHealthTimeout = 3 * time.Minute
)

// UpdateInfo tells the UI whether a newer manager version is published and
// whether the manager can install it itself.
type UpdateInfo struct {
	Current   string     `json:"current"`
	Latest    string     `json:"latest,omitempty"`
	Available bool       `json:"available"`
	CanApply  bool       `json:"canApply"`
	Reason    string     `json:"reason,omitempty"` // why it can't apply
	Updating  bool       `json:"updating"`
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
	Error     string     `json:"error,omitempty"`
}

type updateState struct {
	mu        sync.Mutex
	latest    string
	checkedAt time.Time
	checkErr  string
	applyErr  string
	updating  bool
	// pinned is the x.y.z tag a compose file runs the manager on, which the
	// next `docker compose up` would bring back after an update.
	pinned    string
	inspected bool
	// notified is the latest version already announced to channels.
	notified string
}

var versionRe = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`)

func parseVersion(v string) ([3]int, bool) {
	m := versionRe.FindStringSubmatch(v)
	if m == nil {
		return [3]int{}, false
	}
	var out [3]int
	for i := range out {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out, true
}

// newer reports whether version a is strictly above b (both x.y.z).
func newer(a, b string) bool {
	va, ok1 := parseVersion(a)
	vb, ok2 := parseVersion(b)
	if !ok1 || !ok2 {
		return false
	}
	for i := range va {
		if va[i] != vb[i] {
			return va[i] > vb[i]
		}
	}
	return false
}

// latestVersion is the highest x.y.z tag; others (latest, sha-...) are ignored.
func latestVersion(tags []string) string {
	var best string
	for _, t := range tags {
		if _, ok := parseVersion(t); ok && (best == "" || newer(t, best)) {
			best = t
		}
	}
	return best
}

// StartUpdateChecker looks for a newer published version now and every six
// hours. Development builds (no x.y.z version) never check.
func (c *Core) StartUpdateChecker() error {
	if !c.cfg.Update.Check {
		return nil
	}
	if _, ok := parseVersion(c.cfg.Version); !ok {
		c.log.Debug("update checks are off for development builds", "version", c.cfg.Version)
		return nil
	}
	return c.goLoop(func() {
		timer := time.NewTimer(0)
		defer timer.Stop()
		for {
			select {
			case <-c.bg.Done():
				return
			case <-timer.C:
				c.checkUpdate(c.bg)
				timer.Reset(updateEvery)
			}
		}
	})
}

// CheckUpdate looks for a newer published version now, on demand.
func (c *Core) CheckUpdate(ctx context.Context) UpdateInfo {
	c.checkUpdate(ctx)
	return c.Update()
}

func (c *Core) checkUpdate(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	c.inspectPin(ctx)
	tags, err := registry.Tags(ctx, http.DefaultClient, c.cfg.Update.Image)

	u := &c.update
	u.mu.Lock()
	defer u.mu.Unlock()
	u.checkedAt = time.Now()
	if err != nil {
		u.checkErr = "update check failed: " + err.Error()
		c.log.Warn("update check failed", "image", c.cfg.Update.Image, "err", err)
		return
	}
	u.checkErr = ""
	u.latest = latestVersion(tags)
	if newer(u.latest, c.cfg.Version) {
		c.log.Info("a new version is available", "current", c.cfg.Version, "latest", u.latest)
		if u.notified != u.latest {
			u.notified = u.latest
			c.notify(notify.Event{
				Type:    EventUpdateAvailable,
				Level:   notify.Info,
				Title:   "kipitiny " + u.latest + " is available",
				Message: "Running " + c.cfg.Version + ". Update from the sidebar or Settings › Version.",
			}, "/settings", "")
		}
	}
}

// inspectPin records once whether compose pins the manager's version: the
// container's image can't change while this process runs.
func (c *Core) inspectPin(ctx context.Context) {
	u := &c.update
	u.mu.Lock()
	done := u.inspected
	u.mu.Unlock()
	self := docker.SelfContainerID()
	if done || self == "" {
		return
	}
	res, err := c.dockerFor(store.LocalServerID).ContainerInspect(ctx, self, client.ContainerInspectOptions{})
	if err != nil {
		c.log.Warn("cannot inspect the manager's container", "err", err)
		return
	}
	pinned := composePin(res.Container.Config.Image, res.Container.Config.Labels)
	u.mu.Lock()
	u.pinned, u.inspected = pinned, true
	u.mu.Unlock()
}

// composePin is the x.y.z tag of ref when a compose file created the
// container from it, empty otherwise. A floating tag isn't a pin: the updater
// retags it (see launchUpdater).
func composePin(ref string, labels map[string]string) string {
	if labels["com.docker.compose.project"] == "" {
		return ""
	}
	ref, _, _ = strings.Cut(ref, "@")
	i := strings.LastIndex(ref, ":")
	if i < 0 || strings.Contains(ref[i:], "/") {
		return ""
	}
	if tag := ref[i+1:]; versionRe.MatchString(tag) {
		return tag
	}
	return ""
}

// Update reports the known update state; it never calls the registry.
func (c *Core) Update() UpdateInfo {
	u := &c.update
	u.mu.Lock()
	defer u.mu.Unlock()
	info := UpdateInfo{
		Current:   c.cfg.Version,
		Latest:    u.latest,
		Available: newer(u.latest, c.cfg.Version),
		Updating:  u.updating,
		Error:     u.applyErr,
	}
	if info.Error == "" {
		info.Error = u.checkErr
	}
	if !u.checkedAt.IsZero() {
		t := u.checkedAt
		info.CheckedAt = &t
	}
	switch {
	case docker.SelfContainerID() == "":
		info.Reason = "the manager isn't running in a container: update it the way it was installed"
	case u.pinned != "":
		info.Reason = "the compose file pins version " + u.pinned + " (KIPITINY_VERSION), which its next up would " +
			"bring back: set it to the new version, or remove it to follow latest, then run docker compose up -d"
	default:
		info.CanApply = true
	}
	return info
}

// ApplyUpdate installs the latest version: it pulls the image, then starts a
// short-lived updater container that replaces this manager's container (see
// docker.ReplaceContainer) and restores it if the new one isn't healthy.
// Apps, Traefik and databases keep running throughout.
func (c *Core) ApplyUpdate(ctx context.Context) (UpdateInfo, error) {
	self := docker.SelfContainerID()
	if self == "" {
		return UpdateInfo{}, fmt.Errorf("%w: the manager isn't running in a container", ErrInvalid)
	}
	u := &c.update
	u.mu.Lock()
	if u.updating {
		u.mu.Unlock()
		return UpdateInfo{}, fmt.Errorf("%w: an update is already running", ErrBusy)
	}
	latest := u.latest
	if u.pinned != "" {
		u.mu.Unlock()
		return UpdateInfo{}, fmt.Errorf("%w: the compose file pins version %s", ErrInvalid, u.pinned)
	}
	if !newer(latest, c.cfg.Version) {
		u.mu.Unlock()
		return UpdateInfo{}, fmt.Errorf("%w: already up to date", ErrInvalid)
	}
	u.updating, u.applyErr = true, ""
	u.mu.Unlock()

	fail := func(err error) {
		c.log.Error("update failed", "version", latest, "err", err)
		u.mu.Lock()
		u.updating, u.applyErr = false, "update to "+latest+" failed: "+err.Error()
		u.mu.Unlock()
	}
	ref := c.cfg.Update.Image + ":" + latest
	if err := c.goBackground(func() {
		if err := c.launchUpdater(c.bg, self, ref); err != nil {
			fail(err)
		}
	}); err != nil {
		fail(err)
		return UpdateInfo{}, err
	}
	return c.Update(), nil
}

func (c *Core) launchUpdater(ctx context.Context, self, ref string) error {
	dk := c.dockerFor(store.LocalServerID)
	c.log.Info("updating the manager", "image", ref)
	if err := dk.PullImage(ctx, ref, c.registryAuth(ctx, ref), io.Discard); err != nil {
		return fmt.Errorf("pull %s: %w", ref, err)
	}
	// A compose file on :latest (or no tag) would otherwise bring back the
	// old image on its next `up`, since that's what the tag still names here.
	if res, err := dk.ContainerInspect(ctx, self, client.ContainerInspectOptions{}); err == nil {
		if running := res.Container.Config.Image; floatingTag(running, c.cfg.Update.Image) {
			if _, err := dk.ImageTag(ctx, client.ImageTagOptions{Source: ref, Target: running}); err != nil {
				return fmt.Errorf("tag %s: %w", running, err)
			}
		}
	}
	old, err := dk.ListContainers(ctx, map[string]string{docker.LabelComponent: updaterComponent})
	if err != nil {
		return err
	}
	for _, ct := range old {
		if err := dk.RemoveContainer(ctx, ct.ID, 0); err != nil {
			return err
		}
	}
	// The new version's binary does the swap, so fixes to it apply at once.
	_, err = dk.Run(ctx, client.ContainerCreateOptions{
		Name: updaterName,
		Config: &container.Config{
			Image:       ref,
			Entrypoint:  []string{"/kipitiny"},
			Cmd:         []string{"self-update", self, ref},
			Healthcheck: &container.HealthConfig{Test: []string{"NONE"}},
			Labels: map[string]string{
				docker.LabelManaged:   "true",
				docker.LabelComponent: updaterComponent,
			},
		},
		HostConfig: &container.HostConfig{
			Mounts: []mount.Mount{
				{Type: mount.TypeBind, Source: c.cfg.Traefik.DockerSocket, Target: "/var/run/docker.sock"},
			},
			LogConfig: docker.DefaultLogConfig(),
		},
	})
	return err
}

// floatingTag reports whether ref is image without tag or tagged latest.
func floatingTag(ref, image string) bool {
	return ref == image || ref == image+":latest"
}

// cleanupUpdater removes finished updater containers, copying their output
// into the manager's log first: it is the only record of how an update went.
func (c *Core) cleanupUpdater(ctx context.Context, dk *docker.Client) {
	cts, err := dk.ListContainers(ctx, map[string]string{docker.LabelComponent: updaterComponent})
	if err != nil {
		return
	}
	for _, ct := range cts {
		if ct.State == container.StateRunning {
			continue
		}
		if lines, err := containerLogs(ctx, dk, ct, client.ContainerLogsOptions{Tail: "50"}); err == nil {
			for _, l := range lines {
				c.log.Info("updater: " + strings.TrimSpace(l.Text))
			}
		}
		if err := dk.RemoveContainer(ctx, ct.ID, 0); err != nil && !errors.Is(err, context.Canceled) {
			c.log.Warn("cannot remove updater container", "err", err)
		}
	}
}
