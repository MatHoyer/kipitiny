package monitoring

import (
	"context"

	"github.com/kipit-labs/kipitiny/internal/analytics"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

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
func (c *Monitoring) Trackers(ctx context.Context) ([]analytics.Tracker, error) {
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
func (c *Monitoring) CreateTracker(ctx context.Context, t analytics.Tracker) (analytics.Tracker, error) {
	cl, st, err := c.analyticsOn(ctx)
	if err != nil {
		return t, err
	}
	t, err = cl.CreateTracker(ctx, t)
	return withURL(t, st.Domain), analyticsErr(err, "tracker")
}

// UpdateTracker changes a tracker's name, slug (kept when empty) and target.
func (c *Monitoring) UpdateTracker(ctx context.Context, t analytics.Tracker) (analytics.Tracker, error) {
	cl, st, err := c.analyticsOn(ctx)
	if err != nil {
		return t, err
	}
	id := t.ID
	t, err = cl.UpdateTracker(ctx, t)
	return withURL(t, st.Domain), analyticsErr(err, "tracker "+id)
}

// DeleteTracker removes a tracker and its hits.
func (c *Monitoring) DeleteTracker(ctx context.Context, id string) error {
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

func (c *Monitoring) TrackerAnalytics(ctx context.Context, id string, q AnalyticsQuery) (TrackerView, error) {
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

// serviceScript is svc's tag: from its own domain when it has its own paths.
func serviceScript(svc domain.Service, domain string) string {
	if svc.AnalyticsScript != "" {
		return `<script defer src="https://` + svc.Domain + svc.AnalyticsScript + `"></script>`
	}
	return AnalyticsScript(domain)
}
