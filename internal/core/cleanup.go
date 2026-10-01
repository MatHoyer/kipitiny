package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"github.com/robfig/cron/v3"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/notify"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	cleanupSetting    = "cleanup"
	cleanupRunSetting = "cleanup_last_run"
	// minKeepDeployments keeps enough history for rollbacks.
	minKeepDeployments = 10
)

// Image and volume cleanup modes.
const (
	CleanOff       = "off"
	CleanDangling  = "dangling"  // images: untagged layers only
	CleanAnonymous = "anonymous" // volumes: unnamed only
	CleanUnused    = "unused"    // anything no container uses
)

// CleanupSettings is what the admin enables. Everything the manager relies
// on is protected whatever the settings: database volumes, the images of
// services and of their recent deployments (rollback), and containers and
// networks labelled kipitiny.managed.
type CleanupSettings struct {
	Enabled bool `json:"enabled"`
	// Cron is when it runs (UTC unless CRON_TZ=<zone> is given).
	Cron string `json:"cron"`
	// MinAgeHours leaves anything younger alone, so a deploy in progress
	// never loses its freshly built image.
	MinAgeHours int    `json:"minAgeHours"`
	Images      string `json:"images"`  // off, dangling, unused
	Volumes     string `json:"volumes"` // off, anonymous, unused
	BuildCache  bool   `json:"buildCache"`
	// Containers removes stopped containers the manager does not own.
	Containers bool `json:"containers"`
	// Networks removes unused networks the manager does not own.
	Networks bool `json:"networks"`
	// KeepDeployments trims each service's deployment history and logs to
	// this many entries; 0 keeps everything.
	KeepDeployments int `json:"keepDeployments"`
}

func defaultCleanupSettings() CleanupSettings {
	return CleanupSettings{
		Cron:        "0 4 * * *",
		MinAgeHours: 24,
		Images:      CleanDangling,
		Volumes:     CleanAnonymous,
		BuildCache:  true,
		Networks:    true,
	}
}

// CleanupRun is the outcome of one cleanup.
type CleanupRun struct {
	Trigger    string          `json:"trigger"` // schedule, manual
	StartedAt  time.Time       `json:"startedAt"`
	FinishedAt *time.Time      `json:"finishedAt,omitempty"`
	Servers    []CleanupResult `json:"servers"`
	// Deployments is how many old deployment records (and logs) went away.
	Deployments int    `json:"deployments"`
	Error       string `json:"error,omitempty"`
}

// problems lists the run's errors, prefixed by their server.
func (r CleanupRun) problems() []string {
	var out []string
	if r.Error != "" {
		out = append(out, r.Error)
	}
	for _, s := range r.Servers {
		for _, e := range s.Errors {
			out = append(out, s.Server+": "+e)
		}
	}
	return out
}

// CleanupResult counts what was removed on one server.
type CleanupResult struct {
	Server     string `json:"server"`
	Containers int    `json:"containers"`
	Images     int    `json:"images"`
	Volumes    int    `json:"volumes"`
	Networks   int    `json:"networks"`
	BuildCache int    `json:"buildCache"`
	// Reclaimed is approximate: Docker shares layers between images.
	Reclaimed uint64   `json:"reclaimed"`
	Errors    []string `json:"errors,omitempty"`
}

type CleanupView struct {
	Settings CleanupSettings `json:"settings"`
	Running  bool            `json:"running"`
	NextRun  *time.Time      `json:"nextRun,omitempty"`
	LastRun  *CleanupRun     `json:"lastRun,omitempty"`
}

func (c *Core) cleanupSettings(ctx context.Context) CleanupSettings {
	s := defaultCleanupSettings()
	if v, err := c.store.GetSetting(ctx, cleanupSetting); err == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &s)
	}
	return s
}

func (c *Core) Cleanup(ctx context.Context) CleanupView {
	v := CleanupView{Settings: c.cleanupSettings(ctx), Running: c.cleaning.Load()}
	c.sched.mu.Lock()
	if c.sched.cron != nil && c.sched.cleanup != 0 {
		if next := c.sched.cron.Entry(c.sched.cleanup).Next; !next.IsZero() {
			v.NextRun = &next
		}
	}
	c.sched.mu.Unlock()
	if raw, err := c.store.GetSetting(ctx, cleanupRunSetting); err == nil && raw != "" {
		var run CleanupRun
		if json.Unmarshal([]byte(raw), &run) == nil {
			v.LastRun = &run
		}
	}
	return v
}

func (c *Core) SetCleanup(ctx context.Context, s CleanupSettings) (CleanupView, error) {
	s.Cron = strings.TrimSpace(s.Cron)
	if _, err := cronParser.Parse(s.Cron); err != nil {
		return CleanupView{}, fmt.Errorf("%w: schedule: %v", ErrInvalid, err)
	}
	if s.MinAgeHours < 1 || s.MinAgeHours > 24*365 {
		return CleanupView{}, fmt.Errorf("%w: minimum age must be between 1 hour and a year", ErrInvalid)
	}
	if !oneOf(s.Images, CleanOff, CleanDangling, CleanUnused) {
		return CleanupView{}, fmt.Errorf("%w: images must be off, dangling or unused", ErrInvalid)
	}
	if !oneOf(s.Volumes, CleanOff, CleanAnonymous, CleanUnused) {
		return CleanupView{}, fmt.Errorf("%w: volumes must be off, anonymous or unused", ErrInvalid)
	}
	if s.KeepDeployments != 0 && (s.KeepDeployments < minKeepDeployments || s.KeepDeployments > maxKeep) {
		return CleanupView{}, fmt.Errorf("%w: keep 0 (all) or %d to %d deployments", ErrInvalid, minKeepDeployments, maxKeep)
	}
	b, err := json.Marshal(s)
	if err != nil {
		return CleanupView{}, err
	}
	if err := c.store.SetSetting(ctx, cleanupSetting, string(b)); err != nil {
		return CleanupView{}, err
	}
	if err := c.scheduleCleanup(ctx); err != nil {
		return CleanupView{}, err
	}
	return c.Cleanup(ctx), nil
}

func oneOf(s string, options ...string) bool {
	for _, o := range options {
		if s == o {
			return true
		}
	}
	return false
}

// scheduleCleanup (re)registers the cleanup job from the stored settings.
func (c *Core) scheduleCleanup(ctx context.Context) error {
	s := c.cleanupSettings(ctx)
	c.sched.mu.Lock()
	defer c.sched.mu.Unlock()
	if c.sched.cron == nil {
		return nil // scheduler not started (e.g. tests)
	}
	if c.sched.cleanup != 0 {
		c.sched.cron.Remove(c.sched.cleanup)
		c.sched.cleanup = 0
	}
	if !s.Enabled {
		return nil
	}
	job := cron.FuncJob(func() {
		done := make(chan struct{})
		if err := c.startCleanup(c.bg, "schedule", done); err != nil {
			c.log.Warn("scheduled cleanup did not start", "err", err)
			return
		}
		<-done
	})
	entry, err := c.sched.cron.AddJob(s.Cron, job)
	if err != nil {
		c.log.Error("invalid cleanup schedule", "cron", s.Cron, "err", err)
		return nil
	}
	c.sched.cleanup = entry
	return nil
}

// RunCleanup starts a cleanup with the saved settings now, enabled or not.
func (c *Core) RunCleanup(ctx context.Context) (CleanupView, error) {
	if err := c.startCleanup(ctx, "manual", nil); err != nil {
		return CleanupView{}, err
	}
	return c.Cleanup(ctx), nil
}

func (c *Core) startCleanup(ctx context.Context, trigger string, done chan<- struct{}) error {
	if !c.cleaning.CompareAndSwap(false, true) {
		return ErrBusy
	}
	s := c.cleanupSettings(ctx)
	err := c.goBackground(func() {
		defer c.cleaning.Store(false)
		if done != nil {
			defer close(done)
		}
		c.runCleanup(s, trigger)
	})
	if err != nil {
		c.cleaning.Store(false)
	}
	return err
}

func (c *Core) runCleanup(s CleanupSettings, trigger string) {
	ctx, cancel := context.WithTimeout(c.bg, time.Hour)
	defer cancel()
	run := CleanupRun{Trigger: trigger, StartedAt: time.Now().UTC(), Servers: []CleanupResult{}}

	servers, err := c.store.ListServers(ctx)
	svcs, serr := c.store.ListAllServices(ctx)
	if err = errors.Join(err, serr); err != nil {
		run.Error = err.Error()
	} else {
		for _, sv := range servers {
			run.Servers = append(run.Servers, c.cleanServer(ctx, sv, svcs, s))
		}
		if s.KeepDeployments > 0 {
			run.Deployments = c.trimDeployments(ctx, svcs, s.KeepDeployments)
		}
	}
	finished := time.Now().UTC()
	run.FinishedAt = &finished
	if problems := run.problems(); len(problems) > 0 {
		c.notify(notify.Event{
			Type:    EventCleanupFailed,
			Level:   notify.Warning,
			Title:   "Docker cleanup had errors",
			Message: strings.Join(problems, "\n"),
		}, "/settings", "")
	}
	c.log.Info("cleanup finished", "trigger", trigger, "servers", len(run.Servers), "deployments", run.Deployments)
	if b, err := json.Marshal(run); err == nil {
		if err := c.store.SetSetting(context.WithoutCancel(ctx), cleanupRunSetting, string(b)); err != nil {
			c.log.Warn("cannot record cleanup", "err", err)
		}
	}
}

// cleanServer removes what the settings allow on one server. Each step runs
// even if an earlier one failed.
func (c *Core) cleanServer(ctx context.Context, sv store.Server, svcs []store.Service, s CleanupSettings) CleanupResult {
	r := CleanupResult{Server: sv.Name}
	dk := c.dockerFor(sv.ID)
	until := fmt.Sprintf("%dh", s.MinAgeHours)
	fail := func(what string, err error) {
		if err != nil {
			r.Errors = append(r.Errors, fmt.Sprintf("%s: %v", what, err))
		}
	}
	if _, err := dk.Ping(ctx, client.PingOptions{}); err != nil {
		fail("docker", err)
		return r
	}

	// Containers first: what they held becomes unused for the next steps.
	if s.Containers {
		f := make(client.Filters).Add("until", until).Add("label!", docker.LabelManaged)
		res, err := dk.ContainerPrune(ctx, client.ContainerPruneOptions{Filters: f})
		fail("containers", err)
		r.Containers += len(res.Report.ContainersDeleted)
		r.Reclaimed += res.Report.SpaceReclaimed
	}
	switch s.Images {
	case CleanDangling:
		f := make(client.Filters).Add("dangling", "true").Add("until", until)
		res, err := dk.ImagePrune(ctx, client.ImagePruneOptions{Filters: f})
		fail("images", err)
		r.Images += len(res.Report.ImagesDeleted)
		r.Reclaimed += res.Report.SpaceReclaimed
	case CleanUnused:
		n, size, err := c.removeUnusedImages(ctx, dk, sv.ID, svcs, time.Duration(s.MinAgeHours)*time.Hour)
		fail("images", err)
		r.Images += n
		r.Reclaimed += size
	}
	if s.BuildCache {
		f := make(client.Filters).Add("until", until)
		res, err := dk.BuildCachePrune(ctx, client.BuildCachePruneOptions{All: true, Filters: f})
		fail("build cache", err)
		r.BuildCache += len(res.Report.CachesDeleted)
		r.Reclaimed += res.Report.SpaceReclaimed
	}
	if s.Volumes != CleanOff {
		n, size, err := c.removeUnusedVolumes(ctx, dk, svcs, s.Volumes == CleanUnused, time.Duration(s.MinAgeHours)*time.Hour)
		fail("volumes", err)
		r.Volumes += n
		r.Reclaimed += size
	}
	// Health probe volumes of earlier manager versions, once no app uses them.
	n, err := c.removeStaleProbes(ctx, dk)
	fail("probe volumes", err)
	r.Volumes += n
	if s.Networks {
		f := make(client.Filters).Add("until", until).Add("label!", docker.LabelManaged)
		res, err := dk.NetworkPrune(ctx, client.NetworkPruneOptions{Filters: f})
		fail("networks", err)
		r.Networks += len(res.Report.NetworksDeleted)
	}
	if len(r.Errors) > 0 {
		c.log.Warn("cleanup errors", "server", sv.Name, "errors", r.Errors)
	}
	return r
}

// removeUnusedImages removes images no container uses, except the images of
// the server's services and of their recent successful deployments, which
// deploys and rollbacks need.
func (c *Core) removeUnusedImages(ctx context.Context, dk *docker.Client, serverID string, svcs []store.Service, minAge time.Duration) (int, uint64, error) {
	keep := map[string]bool{}
	protect := func(ref string) {
		if ref == "" {
			return
		}
		if res, err := dk.ImageInspect(ctx, ref); err == nil {
			keep[res.ID] = true
		}
	}
	for _, svc := range svcs {
		if svc.ServerID != serverID {
			continue
		}
		protect(svc.Image)
		deps, err := c.store.ListDeployments(ctx, svc.ID, 50)
		if err != nil {
			return 0, 0, err
		}
		kept := 0
		for _, d := range deps {
			if d.ID == svc.CurrentDeploymentID || d.Status == store.DeploymentRunning ||
				(d.Status == store.DeploymentSucceeded && kept < keepBuilds) {
				protect(d.Image)
				if d.Status == store.DeploymentSucceeded {
					kept++
				}
			}
		}
	}
	cts, err := dk.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return 0, 0, err
	}
	for _, ct := range cts.Items {
		keep[ct.ImageID] = true
	}
	imgs, err := dk.ImageList(ctx, client.ImageListOptions{})
	if err != nil {
		return 0, 0, err
	}
	n, size := 0, uint64(0)
	for _, img := range imgs.Items {
		if keep[img.ID] || time.Since(time.Unix(img.Created, 0)) < minAge {
			continue
		}
		// No force: an image a container started meanwhile stays.
		_, err := dk.ImageRemove(ctx, img.ID, client.ImageRemoveOptions{PruneChildren: true})
		if err != nil {
			if !cerrdefs.IsNotFound(err) && !cerrdefs.IsConflict(err) {
				return n, size, err
			}
			continue
		}
		n++
		size += uint64(max(img.Size, 0))
	}
	return n, size, nil
}

// removeUnusedVolumes removes volumes no container mounts: anonymous ones
// only, or named ones too. Volumes of the manager (labelled, kipitiny-*) and
// database volumes are never touched.
func (c *Core) removeUnusedVolumes(ctx context.Context, dk *docker.Client, svcs []store.Service, named bool, minAge time.Duration) (int, uint64, error) {
	protected := map[string]bool{}
	for _, svc := range svcs {
		if svc.Kind == store.ServiceKindPostgres {
			protected[PostgresVolume(svc.ID)] = true
		}
	}
	f := make(client.Filters).Add("dangling", "true")
	vols, err := dk.VolumeList(ctx, client.VolumeListOptions{Filters: f})
	if err != nil {
		return 0, 0, err
	}
	n, size := 0, uint64(0)
	for _, v := range vols.Items {
		_, managed := v.Labels[docker.LabelManaged]
		if managed || protected[v.Name] || strings.HasPrefix(v.Name, "kipitiny-") {
			continue
		}
		if !named && !isAnonymousVolume(v.Labels) {
			continue
		}
		if created, err := time.Parse(time.RFC3339, v.CreatedAt); err == nil && time.Since(created) < minAge {
			continue
		}
		if _, err := dk.VolumeRemove(ctx, v.Name, client.VolumeRemoveOptions{}); err != nil {
			if !cerrdefs.IsNotFound(err) && !cerrdefs.IsConflict(err) {
				return n, size, err
			}
			continue
		}
		n++
		if v.UsageData != nil && v.UsageData.Size > 0 {
			size += uint64(v.UsageData.Size)
		}
	}
	return n, size, nil
}

// isAnonymousVolume reports whether Docker created the volume without a
// name (it labels those since Engine 23).
func isAnonymousVolume(labels map[string]string) bool {
	_, ok := labels["com.docker.volume.anonymous"]
	return ok
}

// removeStaleProbes removes unused probe volumes left by older manager binaries.
func (c *Core) removeStaleProbes(ctx context.Context, dk *docker.Client) (int, error) {
	_, current, err := ownBinary()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	f := make(client.Filters).Add("dangling", "true").Add("label", docker.LabelComponent+"=probe")
	vols, err := dk.VolumeList(ctx, client.VolumeListOptions{Filters: f})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, v := range vols.Items {
		if current == "" || v.Name == "kipitiny-probe-"+current {
			continue
		}
		if _, err := dk.VolumeRemove(ctx, v.Name, client.VolumeRemoveOptions{}); err == nil {
			n++
		}
	}
	return n, nil
}

// trimDeployments deletes each service's deployments beyond the newest keep,
// with their logs. The current and running ones always stay.
func (c *Core) trimDeployments(ctx context.Context, svcs []store.Service, keep int) int {
	n := 0
	for _, svc := range svcs {
		deps, err := c.store.ListDeployments(ctx, svc.ID, 0) // 0: all
		if err != nil {
			c.log.Warn("cleanup: list deployments", "service", svc.ID, "err", err)
			continue
		}
		for i, d := range deps {
			if i < keep || d.ID == svc.CurrentDeploymentID || d.Status == store.DeploymentRunning {
				continue
			}
			if err := c.store.DeleteDeployment(ctx, d.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
				c.log.Warn("cleanup: delete deployment", "deployment", d.ID, "err", err)
				continue
			}
			if err := os.Remove(c.deployLogPath(svc.ID, d.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
				c.log.Warn("cleanup: delete deployment log", "deployment", d.ID, "err", err)
			}
			n++
		}
	}
	return n
}
