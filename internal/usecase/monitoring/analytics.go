package monitoring

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	dockernet "github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/kipit-labs/kipitiny/internal/analytics"
	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/progress"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

const (
	AnalyticsSetting   = "analytics"
	analyticsName      = "kipitiny-analytics"
	analyticsVolume    = "kipitiny-analytics"
	analyticsComponent = "analytics"
	analyticsRouter    = "kipitiny-analytics"

	defaultAnalyticsRetention = 365
	maxAnalyticsRetention     = 3650
)

// Steps of turning analytics on, reported with progress.Report.
const (
	// AnalyticsStepImage: pulling the server's image, when it's missing.
	AnalyticsStepImage = "image"
	// AnalyticsStepStart: creating and starting its container.
	AnalyticsStepStart = "start"
	// AnalyticsStepHealth: waiting for it to answer.
	AnalyticsStepHealth = "health"
	// AnalyticsStepSites: sending it the domains of the apps that opted in.
	AnalyticsStepSites = "sites"
)

// AnalyticsSettings turn the analytics server on, on the manager's server.
// Apps opt in with domain.Service.Analytics.
type AnalyticsSettings struct {
	Enabled bool `json:"enabled"`
	// Domain serves the tracker script and receives hits.
	Domain        string `json:"domain"`
	RetentionDays int    `json:"retentionDays"`
}

type AnalyticsState struct {
	AnalyticsSettings
	// Token authenticates the manager on the server's private port.
	Token string `json:"token"`
}

func (c *Monitoring) analyticsState(ctx context.Context) AnalyticsState {
	var s AnalyticsState
	if v, err := c.store.GetSetting(ctx, AnalyticsSetting); err == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &s)
	}
	if s.RetentionDays == 0 {
		s.RetentionDays = defaultAnalyticsRetention
	}
	return s
}

func (c *Monitoring) Analytics(ctx context.Context) AnalyticsSettings {
	return c.analyticsState(ctx).AnalyticsSettings
}

// SetAnalytics saves the settings and starts, updates or removes the
// server right away. Turning it off keeps the collected data.
func (c *Monitoring) SetAnalytics(ctx context.Context, in AnalyticsSettings) (AnalyticsSettings, error) {
	in.Domain = strings.ToLower(strings.TrimSpace(in.Domain))
	if in.RetentionDays == 0 {
		in.RetentionDays = defaultAnalyticsRetention
	}
	if in.RetentionDays < 1 || in.RetentionDays > maxAnalyticsRetention {
		return AnalyticsSettings{}, fmt.Errorf("%w: keep analytics 1 to %d days", domain.ErrInvalid, maxAnalyticsRetention)
	}
	if in.Enabled && in.Domain == "" {
		return AnalyticsSettings{}, fmt.Errorf("%w: analytics need a domain for the tracker script", domain.ErrInvalid)
	}
	if in.Domain != "" {
		if !domain.ValidDomain(in.Domain) {
			return AnalyticsSettings{}, fmt.Errorf("%w: invalid domain %q", domain.ErrInvalid, in.Domain)
		}
		if in.Domain == c.cfg.Domain {
			return AnalyticsSettings{}, fmt.Errorf("%w: domain %q is the manager's own", domain.ErrInvalid, in.Domain)
		}
		svcs, err := c.store.ListAllServices(ctx)
		if err != nil {
			return AnalyticsSettings{}, err
		}
		for _, s := range svcs {
			if s.Domain == in.Domain {
				return AnalyticsSettings{}, fmt.Errorf("%w: domain %q is used by %s", domain.ErrInvalid, in.Domain, s.Name)
			}
		}
	}
	if !c.cfg.Traefik.Enabled && in.Enabled {
		return AnalyticsSettings{}, fmt.Errorf("%w: analytics are served through Traefik, which is off", domain.ErrInvalid)
	}
	prev := c.analyticsState(ctx)
	st := prev
	st.AnalyticsSettings = in
	if st.Token == "" {
		b := make([]byte, 32)
		_, _ = rand.Read(b)
		st.Token = hex.EncodeToString(b)
	}
	if err := c.saveAnalytics(ctx, st); err != nil {
		return AnalyticsSettings{}, err
	}
	c.mgr.KickDNS()
	if err := c.EnsureAnalytics(ctx); err != nil {
		// Back to how it was, so a failed enable leaves analytics off
		// instead of retried by the reconciler.
		bg := context.WithoutCancel(ctx)
		if rerr := c.saveAnalytics(bg, prev); rerr == nil {
			_ = c.EnsureAnalytics(bg)
		}
		return AnalyticsSettings{}, fmt.Errorf("%w: the analytics server didn't start: %s", domain.ErrInvalid, err.Error())
	}
	return in, nil
}

func (c *Monitoring) saveAnalytics(ctx context.Context, st AnalyticsState) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return c.store.SetSetting(ctx, AnalyticsSetting, string(b))
}

// DeleteAnalyticsData removes the server's volume, once analytics are off.
func (c *Monitoring) DeleteAnalyticsData(ctx context.Context) error {
	if c.Analytics(ctx).Enabled {
		return fmt.Errorf("%w: turn analytics off first", domain.ErrInvalid)
	}
	dk := c.mgr.DockerFor(domain.LocalServerID)
	if err := c.removeAnalytics(ctx, dk); err != nil {
		return err
	}
	return dk.RemoveVolume(ctx, analyticsVolume)
}

// analyticsPathRe is an app's own tracker path: no "/", no Traefik rule
// syntax, nothing to escape in the script.
var analyticsPathRe = regexp.MustCompile(`^/[A-Za-z0-9._~-][A-Za-z0-9._~/-]{0,99}$`)

func ValidateAnalyticsPaths(script, hit string) error {
	if script == "" && hit == "" {
		return nil
	}
	if !analyticsPathRe.MatchString(script) || !analyticsPathRe.MatchString(hit) {
		return fmt.Errorf("%w: tracker paths: both, each a path like /js/app.js (letters, digits and . _ ~ - /, up to 100)", domain.ErrInvalid)
	}
	if script == hit {
		return fmt.Errorf("%w: tracker paths: the script and the hits need different paths", domain.ErrInvalid)
	}
	return nil
}

// trackedApps are the apps the analytics server counts.
func (c *Monitoring) trackedApps(ctx context.Context) ([]domain.Service, error) {
	svcs, err := c.store.ListAllServices(ctx)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(svcs, func(s domain.Service) bool {
		return !s.Analytics || s.ServerID != domain.LocalServerID || !network.HTTPRouted(s)
	}), nil
}

// ownPathsPriority puts an app's tracker paths above its own routers (and
// the pages shown while it's down or asleep), below maintenance.
const ownPathsPriority = 1 << 29

func (c *Monitoring) analyticsSpec(ctx context.Context, st AnalyticsState, apps []domain.Service) client.ContainerCreateOptions {
	labels := map[string]string{
		domain.LabelManaged:      "true",
		domain.LabelComponent:    analyticsComponent,
		"traefik.enable":         "true",
		"traefik.docker.network": domain.ProxyNetwork,
		"traefik.http.routers." + analyticsRouter + ".rule":                      "Host(`" + st.Domain + "`)",
		"traefik.http.routers." + analyticsRouter + ".entrypoints":               "websecure",
		"traefik.http.routers." + analyticsRouter + ".service":                   analyticsRouter,
		"traefik.http.services." + analyticsRouter + ".loadbalancer.server.port": strconv.Itoa(analytics.PublicPort),
	}
	if r := c.mgr.CertResolver(ctx, domain.LocalServerID, st.Domain); r != "" {
		labels["traefik.http.routers."+analyticsRouter+".tls.certresolver"] = r
	} else {
		labels["traefik.http.routers."+analyticsRouter+".tls"] = "true"
	}
	// Apps with their own paths: those two paths of their domain reach the
	// server, which tells them apart by the Host.
	for _, s := range apps {
		if s.AnalyticsScript == "" {
			continue
		}
		r := analyticsRouter + "-" + strings.ToLower(s.ID)
		labels["traefik.http.routers."+r+".rule"] = "Host(`" + s.Domain + "`) && (Path(`" + s.AnalyticsScript + "`) || Path(`" + s.AnalyticsHit + "`))"
		labels["traefik.http.routers."+r+".entrypoints"] = "websecure"
		labels["traefik.http.routers."+r+".service"] = analyticsRouter
		labels["traefik.http.routers."+r+".priority"] = strconv.Itoa(ownPathsPriority)
		if res := c.mgr.CertResolver(ctx, s.ServerID, s.Domain); res != "" {
			labels["traefik.http.routers."+r+".tls.certresolver"] = res
		} else {
			labels["traefik.http.routers."+r+".tls"] = "true"
		}
	}
	return client.ContainerCreateOptions{
		Name: analyticsName,
		Config: &container.Config{
			Image: c.mgr.OwnImageRef(),
			Cmd:   []string{"analytics"},
			// The image runs the manager as root; the server needs none of it
			// (its volume is owned by nonroot, see the Dockerfile).
			User: "65532:65532",
			// The image's own checks the manager's port.
			Healthcheck: &container.HealthConfig{
				Test:     []string{"CMD", "/kipitiny", "analytics", "healthcheck"},
				Interval: 30 * time.Second, Timeout: 5 * time.Second, StartPeriod: 10 * time.Second, Retries: 3,
			},
			// The token in the environment keeps it out of `ps`.
			Env: []string{
				"KIPITINY_ANALYTICS_TOKEN=" + st.Token,
				"KIPITINY_ANALYTICS_RETENTION_DAYS=" + strconv.Itoa(st.RetentionDays),
			},
			Labels: labels,
		},
		HostConfig: &container.HostConfig{
			RestartPolicy:  container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			LogConfig:      docker.DefaultLogConfig(),
			ReadonlyRootfs: true,
			CapDrop:        []string{"ALL"},
			SecurityOpt:    []string{"no-new-privileges"},
			Resources:      container.Resources{Memory: 256 << 20},
			Mounts: []mount.Mount{{
				Type: mount.TypeVolume, Source: analyticsVolume, Target: "/data",
				VolumeOptions: &mount.VolumeOptions{Labels: map[string]string{
					domain.LabelManaged: "true", domain.LabelComponent: analyticsComponent,
				}},
			}},
		},
		NetworkingConfig: &dockernet.NetworkingConfig{
			EndpointsConfig: map[string]*dockernet.EndpointSettings{domain.ProxyNetwork: {}},
		},
	}
}

// EnsureAnalytics runs the analytics server on the manager's server while
// analytics are on (recreated when its configuration or image changes),
// removes it otherwise, and gives it the tracked hostnames.
func (c *Monitoring) EnsureAnalytics(ctx context.Context) error {
	c.analyticsMu.Lock()
	defer c.analyticsMu.Unlock()
	dk := c.mgr.DockerFor(domain.LocalServerID)
	st := c.analyticsState(ctx)
	if !st.Enabled {
		c.analyticsSites, c.analyticsErrors = "", ""
		return c.removeAnalytics(ctx, dk)
	}
	apps, err := c.trackedApps(ctx)
	if err != nil {
		return err
	}
	opts := c.analyticsSpec(ctx, st, apps)
	hash := network.SpecHash(opts.Config.Image, opts.Config.Env, opts.Config.Labels)
	opts.Config.Labels[domain.LabelConfigHash] = hash
	existing, err := dk.ListContainers(ctx, map[string]string{domain.LabelComponent: analyticsComponent})
	if err != nil {
		return err
	}
	var id string
	if len(existing) == 1 && existing[0].Labels[domain.LabelConfigHash] == hash {
		id = existing[0].ID
		if existing[0].State != domain.StateRunning {
			if _, err := dk.ContainerStart(ctx, id, client.ContainerStartOptions{}); err != nil {
				return err
			}
		}
	} else {
		if err := c.removeAnalytics(ctx, dk); err != nil {
			return err
		}
		if _, err := dk.ImageInspect(ctx, opts.Config.Image); err != nil {
			progress.Report(ctx, AnalyticsStepImage)
		}
		if err := dk.EnsureImage(ctx, opts.Config.Image, c.mgr.RegistryAuth(ctx, opts.Config.Image), ""); err != nil {
			return fmt.Errorf("pull %s: %w", opts.Config.Image, err)
		}
		progress.Report(ctx, AnalyticsStepStart)
		if id, err = dk.RunMoby(ctx, opts); err != nil {
			return err
		}
		progress.Report(ctx, AnalyticsStepHealth)
		if err := dk.WaitHealthy(ctx, id, time.Minute); err != nil {
			return err
		}
		c.log.Info("analytics server started", "image", opts.Config.Image, "domain", st.Domain)
		c.analyticsSites, c.analyticsErrors = "", ""
	}
	progress.Report(ctx, AnalyticsStepSites)
	if err := c.syncAnalyticsSites(ctx, id, st.Token, apps); err != nil {
		return err
	}
	return c.syncErrorProjects(ctx, id, st.Token)
}

func (c *Monitoring) removeAnalytics(ctx context.Context, dk *docker.Client) error {
	existing, err := dk.ListContainers(ctx, map[string]string{domain.LabelComponent: analyticsComponent})
	if err != nil {
		return err
	}
	for _, ct := range existing {
		c.log.Info("removing analytics server", "id", ct.ID[:12])
		if err := dk.RemoveContainer(ctx, ct.ID, 10*time.Second); err != nil {
			return err
		}
	}
	return nil
}

// syncAnalyticsSites sends the opted-in apps' domains and own paths when
// they changed since the last time (or the container is new).
func (c *Monitoring) syncAnalyticsSites(ctx context.Context, id, token string, apps []domain.Service) error {
	hosts := []string{}
	paths := map[string]analytics.Paths{}
	for _, s := range apps {
		hosts = append(hosts, s.Domain)
		if s.AnalyticsScript != "" {
			paths[s.Domain] = analytics.Paths{Script: s.AnalyticsScript, Hit: s.AnalyticsHit}
		}
	}
	slices.Sort(hosts)
	key := id + " " + strings.Join(hosts, " ")
	for _, h := range hosts {
		if p, ok := paths[h]; ok {
			key += " " + h + p.Script + " " + p.Hit
		}
	}
	if key == c.analyticsSites {
		return nil
	}
	cl, err := c.analyticsClient(ctx, token)
	if err != nil {
		return err
	}
	if err := cl.SetSites(ctx, hosts, paths); err != nil {
		return err
	}
	c.analyticsSites = key
	return nil
}

// analyticsClient reaches the server's private port at its address on the
// proxy network, which the manager is on (or, run on the host, routes to).
func (c *Monitoring) analyticsClient(ctx context.Context, token string) (*analytics.Client, error) {
	cts, err := c.mgr.DockerFor(domain.LocalServerID).ListContainers(ctx, map[string]string{domain.LabelComponent: analyticsComponent})
	if err != nil {
		return nil, err
	}
	for _, ct := range cts {
		if ct.State != domain.StateRunning {
			continue
		}
		if ip := ct.Networks[domain.ProxyNetwork].IP; ip.IsValid() {
			return &analytics.Client{BaseURL: "http://" + ip.String() + ":" + strconv.Itoa(analytics.PrivatePort), Token: token}, nil
		}
	}
	return nil, errors.New("the analytics server isn't running")
}
