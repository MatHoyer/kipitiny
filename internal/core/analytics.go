package core

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
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/analytics"
	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/progress"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	analyticsSetting   = "analytics"
	analyticsName      = "kipitiny-analytics"
	analyticsVolume    = "kipitiny-analytics"
	analyticsComponent = "analytics"
	analyticsImage     = "ghcr.io/mathoyer/kipitiny-analytics"
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
// Apps opt in with store.Service.Analytics.
type AnalyticsSettings struct {
	Enabled bool `json:"enabled"`
	// Domain serves the tracker script and receives hits.
	Domain        string `json:"domain"`
	RetentionDays int    `json:"retentionDays"`
}

type analyticsState struct {
	AnalyticsSettings
	// Token authenticates the manager on the server's private port.
	Token string `json:"token"`
}

func (c *Core) analyticsState(ctx context.Context) analyticsState {
	var s analyticsState
	if v, err := c.store.GetSetting(ctx, analyticsSetting); err == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &s)
	}
	if s.RetentionDays == 0 {
		s.RetentionDays = defaultAnalyticsRetention
	}
	return s
}

func (c *Core) Analytics(ctx context.Context) AnalyticsSettings {
	return c.analyticsState(ctx).AnalyticsSettings
}

// SetAnalytics saves the settings and starts, updates or removes the
// server right away. Turning it off keeps the collected data.
func (c *Core) SetAnalytics(ctx context.Context, in AnalyticsSettings) (AnalyticsSettings, error) {
	in.Domain = strings.ToLower(strings.TrimSpace(in.Domain))
	if in.RetentionDays == 0 {
		in.RetentionDays = defaultAnalyticsRetention
	}
	if in.RetentionDays < 1 || in.RetentionDays > maxAnalyticsRetention {
		return AnalyticsSettings{}, fmt.Errorf("%w: keep analytics 1 to %d days", ErrInvalid, maxAnalyticsRetention)
	}
	if in.Enabled && in.Domain == "" {
		return AnalyticsSettings{}, fmt.Errorf("%w: analytics need a domain for the tracker script", ErrInvalid)
	}
	if in.Domain != "" {
		if !domainRe.MatchString(in.Domain) {
			return AnalyticsSettings{}, fmt.Errorf("%w: invalid domain %q", ErrInvalid, in.Domain)
		}
		if in.Domain == c.cfg.Domain {
			return AnalyticsSettings{}, fmt.Errorf("%w: domain %q is the manager's own", ErrInvalid, in.Domain)
		}
		svcs, err := c.store.ListAllServices(ctx)
		if err != nil {
			return AnalyticsSettings{}, err
		}
		for _, s := range svcs {
			if s.Domain == in.Domain {
				return AnalyticsSettings{}, fmt.Errorf("%w: domain %q is used by %s", ErrInvalid, in.Domain, s.Name)
			}
		}
	}
	if !c.cfg.Traefik.Enabled && in.Enabled {
		return AnalyticsSettings{}, fmt.Errorf("%w: analytics are served through Traefik, which is off", ErrInvalid)
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
	c.kickDNS()
	if err := c.ensureAnalytics(ctx); err != nil {
		// Back to how it was, so a failed enable leaves analytics off
		// instead of retried by the reconciler.
		bg := context.WithoutCancel(ctx)
		if rerr := c.saveAnalytics(bg, prev); rerr == nil {
			_ = c.ensureAnalytics(bg)
		}
		return AnalyticsSettings{}, fmt.Errorf("%w: the analytics server didn't start: %v", ErrInvalid, err)
	}
	return in, nil
}

func (c *Core) saveAnalytics(ctx context.Context, st analyticsState) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return c.store.SetSetting(ctx, analyticsSetting, string(b))
}

// DeleteAnalyticsData removes the server's volume, once analytics are off.
func (c *Core) DeleteAnalyticsData(ctx context.Context) error {
	if c.Analytics(ctx).Enabled {
		return fmt.Errorf("%w: turn analytics off first", ErrInvalid)
	}
	dk := c.dockerFor(store.LocalServerID)
	if err := c.removeAnalytics(ctx, dk); err != nil {
		return err
	}
	return dk.RemoveVolume(ctx, analyticsVolume)
}

var releaseRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// analyticsImageRef is KIPITINY_ANALYTICS_IMAGE, else the image released
// with this version (:dev for builds between releases, `make
// docker-analytics`).
func (c *Core) analyticsImageRef() string {
	if c.cfg.Analytics.Image != "" {
		return c.cfg.Analytics.Image
	}
	if releaseRe.MatchString(c.cfg.Version) {
		return analyticsImage + ":" + c.cfg.Version
	}
	return analyticsImage + ":dev"
}

func (c *Core) analyticsSpec(ctx context.Context, st analyticsState) client.ContainerCreateOptions {
	labels := map[string]string{
		docker.LabelManaged:      "true",
		docker.LabelComponent:    analyticsComponent,
		"traefik.enable":         "true",
		"traefik.docker.network": docker.ProxyNetwork,
		"traefik.http.routers." + analyticsRouter + ".rule":                      "Host(`" + st.Domain + "`)",
		"traefik.http.routers." + analyticsRouter + ".entrypoints":               "websecure",
		"traefik.http.routers." + analyticsRouter + ".service":                   analyticsRouter,
		"traefik.http.services." + analyticsRouter + ".loadbalancer.server.port": strconv.Itoa(analytics.PublicPort),
	}
	if r := c.certResolver(ctx, store.LocalServerID, st.Domain); r != "" {
		labels["traefik.http.routers."+analyticsRouter+".tls.certresolver"] = r
	} else {
		labels["traefik.http.routers."+analyticsRouter+".tls"] = "true"
	}
	return client.ContainerCreateOptions{
		Name: analyticsName,
		Config: &container.Config{
			Image: c.analyticsImageRef(),
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
					docker.LabelManaged: "true", docker.LabelComponent: analyticsComponent,
				}},
			}},
		},
		NetworkingConfig: &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{docker.ProxyNetwork: {}},
		},
	}
}

// ensureAnalytics runs the analytics server on the manager's server while
// analytics are on (recreated when its configuration or image changes),
// removes it otherwise, and gives it the tracked hostnames.
func (c *Core) ensureAnalytics(ctx context.Context) error {
	c.analyticsMu.Lock()
	defer c.analyticsMu.Unlock()
	dk := c.dockerFor(store.LocalServerID)
	st := c.analyticsState(ctx)
	if !st.Enabled {
		c.analyticsSites = ""
		return c.removeAnalytics(ctx, dk)
	}
	opts := c.analyticsSpec(ctx, st)
	hash := specHash(opts.Config.Image, opts.Config.Env, opts.Config.Labels)
	opts.Config.Labels[docker.LabelConfigHash] = hash
	existing, err := dk.ListContainers(ctx, map[string]string{docker.LabelComponent: analyticsComponent})
	if err != nil {
		return err
	}
	var id string
	if len(existing) == 1 && existing[0].Labels[docker.LabelConfigHash] == hash {
		id = existing[0].ID
		if existing[0].State != container.StateRunning {
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
		if err := dk.EnsureImage(ctx, opts.Config.Image, c.registryAuth(ctx, opts.Config.Image)); err != nil {
			return fmt.Errorf("pull %s: %w", opts.Config.Image, err)
		}
		progress.Report(ctx, AnalyticsStepStart)
		if id, err = dk.Run(ctx, opts); err != nil {
			return err
		}
		progress.Report(ctx, AnalyticsStepHealth)
		if err := dk.WaitHealthy(ctx, id, time.Minute); err != nil {
			return err
		}
		c.log.Info("analytics server started", "image", opts.Config.Image, "domain", st.Domain)
		c.analyticsSites = ""
	}
	progress.Report(ctx, AnalyticsStepSites)
	return c.syncAnalyticsSites(ctx, id, st.Token)
}

func (c *Core) removeAnalytics(ctx context.Context, dk *docker.Client) error {
	existing, err := dk.ListContainers(ctx, map[string]string{docker.LabelComponent: analyticsComponent})
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

// syncAnalyticsSites sends the opted-in apps' domains when they changed
// since the last time (or the container is new).
func (c *Core) syncAnalyticsSites(ctx context.Context, id, token string) error {
	svcs, err := c.store.ListAllServices(ctx)
	if err != nil {
		return err
	}
	hosts := []string{}
	for _, s := range svcs {
		if s.Analytics && s.ServerID == store.LocalServerID && httpRouted(s) {
			hosts = append(hosts, s.Domain)
		}
	}
	slices.Sort(hosts)
	key := id + " " + strings.Join(hosts, " ")
	if key == c.analyticsSites {
		return nil
	}
	cl, err := c.analyticsClient(ctx, token)
	if err != nil {
		return err
	}
	if err := cl.SetSites(ctx, hosts); err != nil {
		return err
	}
	c.analyticsSites = key
	return nil
}

// analyticsClient reaches the server's private port at its address on the
// proxy network, which the manager is on (or, run on the host, routes to).
func (c *Core) analyticsClient(ctx context.Context, token string) (*analytics.Client, error) {
	cts, err := c.dockerFor(store.LocalServerID).ListContainers(ctx, map[string]string{docker.LabelComponent: analyticsComponent})
	if err != nil {
		return nil, err
	}
	for _, ct := range cts {
		if ct.State != container.StateRunning || ct.NetworkSettings == nil {
			continue
		}
		if ep := ct.NetworkSettings.Networks[docker.ProxyNetwork]; ep != nil && ep.IPAddress.IsValid() {
			return &analytics.Client{BaseURL: "http://" + ep.IPAddress.String() + ":" + strconv.Itoa(analytics.PrivatePort), Token: token}, nil
		}
	}
	return nil, errors.New("the analytics server isn't running")
}

// AnalyticsQuery is a range of an app's stats: [From, To), in buckets of
// Interval seconds aligned with Offset (seconds east of UTC), narrowed by
// "<breakdown>:<value>" filters.
type AnalyticsQuery struct {
	From, To time.Time
	Interval int
	Offset   int
	Filters  []string
}

// AnalyticsView is an app's stats, and the snippet its pages include.
type AnalyticsView struct {
	analytics.Stats
	Script string `json:"script"`
}

// serviceAnalytics checks the app's analytics are on and returns its
// domain's client.
func (c *Core) serviceAnalytics(ctx context.Context, serviceID string) (store.Service, *analytics.Client, analyticsState, error) {
	svc, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return svc, nil, analyticsState{}, err
	}
	cl, st, err := c.analyticsOn(ctx)
	if err != nil {
		return svc, nil, st, err
	}
	if !svc.Analytics || svc.Domain == "" {
		return svc, nil, st, fmt.Errorf("%w: analytics are off for %s (its settings)", ErrInvalid, svc.Name)
	}
	return svc, cl, st, nil
}

// analyticsOn returns the server's client while analytics are on.
func (c *Core) analyticsOn(ctx context.Context) (*analytics.Client, analyticsState, error) {
	st := c.analyticsState(ctx)
	if !st.Enabled {
		return nil, st, fmt.Errorf("%w: analytics are off (Settings › Analytics)", ErrInvalid)
	}
	cl, err := c.analyticsClient(ctx, st.Token)
	return cl, st, err
}

// analyticsErr turns the server's refusals into core's errors; what names
// what wasn't found.
func analyticsErr(err error, what string) error {
	switch {
	case errors.Is(err, analytics.ErrInvalid):
		return fmt.Errorf("%w: %s", ErrInvalid, strings.TrimPrefix(err.Error(), analytics.ErrInvalid.Error()+": "))
	case errors.Is(err, analytics.ErrNotFound):
		return fmt.Errorf("%w: %s", store.ErrNotFound, what)
	}
	return err
}

func (q AnalyticsQuery) query(hosts ...string) (analytics.Query, error) {
	filters, err := analytics.ParseFilters(q.Filters)
	if err != nil {
		return analytics.Query{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return analytics.Query{
		Hosts: hosts, From: q.From, To: q.To,
		Interval: time.Duration(q.Interval) * time.Second, Offset: q.Offset, Filters: filters,
	}, nil
}

func (c *Core) ServiceAnalytics(ctx context.Context, serviceID string, q AnalyticsQuery) (AnalyticsView, error) {
	svc, cl, st, err := c.serviceAnalytics(ctx, serviceID)
	if err != nil {
		return AnalyticsView{}, err
	}
	aq, err := q.query(svc.Domain)
	if err != nil {
		return AnalyticsView{}, err
	}
	s, err := cl.Stats(ctx, aq)
	if err != nil {
		return AnalyticsView{}, err
	}
	return AnalyticsView{Stats: s, Script: AnalyticsScript(st.Domain)}, nil
}

// ServiceVisits lists the app's visits started in q's range, newest first:
// limit (at most 100) of them, started before the visit before when set.
func (c *Core) ServiceVisits(ctx context.Context, serviceID string, q AnalyticsQuery, before string, limit int) ([]analytics.Visit, error) {
	svc, cl, _, err := c.serviceAnalytics(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	q.Interval = 86400 // only checked by the server
	aq, err := q.query(svc.Domain)
	if err != nil {
		return nil, err
	}
	return cl.Visits(ctx, aq, before, limit)
}

// ServiceVisit is one of the app's visits, with its journey.
func (c *Core) ServiceVisit(ctx context.Context, serviceID, visitID string) (analytics.VisitDetail, error) {
	svc, cl, _, err := c.serviceAnalytics(ctx, serviceID)
	if err != nil {
		return analytics.VisitDetail{}, err
	}
	d, err := cl.Visit(ctx, []string{svc.Domain}, visitID)
	return d, analyticsErr(err, "visit "+visitID)
}

// withURL sets t's public URL on the analytics domain.
func withURL(t analytics.Tracker, domain string) analytics.Tracker {
	if t.Kind == analytics.KindPixel {
		t.URL = "https://" + domain + "/p/" + t.Slug + ".gif"
	} else {
		t.URL = "https://" + domain + "/l/" + t.Slug
	}
	return t
}

// Trackers are the pixels and tracked links, newest first.
func (c *Core) Trackers(ctx context.Context) ([]analytics.Tracker, error) {
	cl, st, err := c.analyticsOn(ctx)
	if err != nil {
		return nil, err
	}
	ts, err := cl.Trackers(ctx)
	for i := range ts {
		ts[i] = withURL(ts[i], st.Domain)
	}
	return ts, analyticsErr(err, "trackers")
}

// CreateTracker adds a pixel or a link (Kind, Name, optional Slug, a
// link's Target).
func (c *Core) CreateTracker(ctx context.Context, t analytics.Tracker) (analytics.Tracker, error) {
	cl, st, err := c.analyticsOn(ctx)
	if err != nil {
		return t, err
	}
	t, err = cl.CreateTracker(ctx, t)
	return withURL(t, st.Domain), analyticsErr(err, "tracker")
}

// UpdateTracker changes a tracker's name, slug (kept when empty) and target.
func (c *Core) UpdateTracker(ctx context.Context, t analytics.Tracker) (analytics.Tracker, error) {
	cl, st, err := c.analyticsOn(ctx)
	if err != nil {
		return t, err
	}
	id := t.ID
	t, err = cl.UpdateTracker(ctx, t)
	return withURL(t, st.Domain), analyticsErr(err, "tracker "+id)
}

// DeleteTracker removes a tracker and its hits.
func (c *Core) DeleteTracker(ctx context.Context, id string) error {
	cl, _, err := c.analyticsOn(ctx)
	if err != nil {
		return err
	}
	return analyticsErr(cl.DeleteTracker(ctx, id), "tracker "+id)
}

// TrackerView is a pixel's or link's stats.
type TrackerView struct {
	analytics.Stats
	Tracker analytics.Tracker `json:"tracker"`
}

func (c *Core) TrackerAnalytics(ctx context.Context, id string, q AnalyticsQuery) (TrackerView, error) {
	cl, st, err := c.analyticsOn(ctx)
	if err != nil {
		return TrackerView{}, err
	}
	t, err := cl.Tracker(ctx, id)
	if err != nil {
		return TrackerView{}, analyticsErr(err, "tracker "+id)
	}
	aq, err := q.query(t.Host())
	if err != nil {
		return TrackerView{}, err
	}
	s, err := cl.Stats(ctx, aq)
	return TrackerView{Stats: s, Tracker: withURL(t, st.Domain)}, analyticsErr(err, "tracker "+id)
}

// AnalyticsScript is the tag a tracked app's pages include.
func AnalyticsScript(domain string) string {
	return `<script defer src="https://` + domain + `/a.js"></script>`
}
