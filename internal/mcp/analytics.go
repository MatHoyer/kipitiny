package mcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MatHoyer/kipitiny/internal/analytics"
	"github.com/MatHoyer/kipitiny/internal/core"
)

type analyticsIn struct {
	Service string   `json:"service,omitempty" jsonschema:"project/service of an app with analytics on"`
	Tracker string   `json:"tracker,omitempty" jsonschema:"instead of service: a pixel's or tracked link's id, from list_trackers (needs admin)"`
	Days    int      `json:"days,omitempty" jsonschema:"how many days back, default 7, at most 730"`
	Filters []string `json:"filters,omitempty" jsonschema:"narrow to breakdown values, as <breakdown>:<value>, e.g. countries:FR, pages:/pricing, referrers:google.com, events:signup, entries:/"`
}

func (t *tools) getAnalytics(ctx context.Context, _ *mcp.CallToolRequest, in analyticsIn) (*mcp.CallToolResult, core.AnalyticsView, error) {
	if err := core.Authenticated(ctx); err != nil {
		return nil, core.AnalyticsView{}, err
	}
	days := in.Days
	if days <= 0 {
		days = 7
	}
	now := time.Now()
	q := core.AnalyticsQuery{From: now.AddDate(0, 0, -min(days, 730)), To: now, Interval: 86400, Filters: in.Filters}
	if in.Tracker != "" {
		if err := core.RequireAdmin(ctx); err != nil {
			return nil, core.AnalyticsView{}, err
		}
		v, err := t.c.TrackerAnalytics(ctx, in.Tracker, q)
		return nil, core.AnalyticsView{Stats: v.Stats, Script: v.Tracker.URL}, friendly(err)
	}
	svc, err := t.service(ctx, in.Service, core.PermView)
	if err != nil {
		return nil, core.AnalyticsView{}, friendly(err)
	}
	out, err := t.c.ServiceAnalytics(ctx, svc.ID, q)
	return nil, out, friendly(err)
}

type trackersOut struct {
	Trackers []analytics.Tracker `json:"trackers"`
}

func (t *tools) listTrackers(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, trackersOut, error) {
	if err := core.RequireAdmin(ctx); err != nil {
		return nil, trackersOut{}, err
	}
	ts, err := t.c.Trackers(ctx)
	return nil, trackersOut{Trackers: ts}, friendly(err)
}

type createTrackerIn struct {
	Kind   string `json:"kind" jsonschema:"pixel (a 1x1 GIF counting views) or link (a redirect counting clicks)"`
	Name   string `json:"name" jsonschema:"what it is for, up to 100 characters"`
	Slug   string `json:"slug,omitempty" jsonschema:"its URL's last part: lowercase letters, digits and -; random by default"`
	Target string `json:"target,omitempty" jsonschema:"a link's destination, an http(s) URL (UTM tags included)"`
}

func (t *tools) createTracker(ctx context.Context, _ *mcp.CallToolRequest, in createTrackerIn) (*mcp.CallToolResult, analytics.Tracker, error) {
	out, err := mutate(ctx, t.c, "create_tracker", in.Name, func() (analytics.Tracker, error) {
		if err := core.RequireAdmin(ctx); err != nil {
			return analytics.Tracker{}, err
		}
		return t.c.CreateTracker(ctx, analytics.Tracker{Kind: in.Kind, Name: in.Name, Slug: in.Slug, Target: in.Target})
	})
	return nil, out, err
}

type deleteTrackerIn struct {
	ID string `json:"id" jsonschema:"the pixel's or link's id, from list_trackers"`
}

func (t *tools) deleteTracker(ctx context.Context, _ *mcp.CallToolRequest, in deleteTrackerIn) (*mcp.CallToolResult, struct{}, error) {
	_, err := mutate(ctx, t.c, "delete_tracker", in.ID, func() (struct{}, error) {
		if err := core.RequireAdmin(ctx); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, t.c.DeleteTracker(ctx, in.ID)
	})
	return nil, struct{}{}, err
}

type visitsIn struct {
	Service string   `json:"service" jsonschema:"project/service of an app with analytics on"`
	Days    int      `json:"days,omitempty" jsonschema:"how many days back, default 7, at most 730"`
	Filters []string `json:"filters,omitempty" jsonschema:"as get_analytics, e.g. identities:user-42 for one identified user's visits"`
	Before  string   `json:"before,omitempty" jsonschema:"a visit id: the visits started before it, for the next page"`
	Limit   int      `json:"limit,omitempty" jsonschema:"at most 100 (default)"`
}

type visitsOut struct {
	Visits []analytics.Visit `json:"visits"`
}

func (t *tools) listVisits(ctx context.Context, _ *mcp.CallToolRequest, in visitsIn) (*mcp.CallToolResult, visitsOut, error) {
	if err := core.Authenticated(ctx); err != nil {
		return nil, visitsOut{}, err
	}
	svc, err := t.service(ctx, in.Service, core.PermView)
	if err != nil {
		return nil, visitsOut{}, friendly(err)
	}
	days := in.Days
	if days <= 0 {
		days = 7
	}
	now := time.Now()
	vs, err := t.c.ServiceVisits(ctx, svc.ID, core.AnalyticsQuery{From: now.AddDate(0, 0, -min(days, 730)), To: now, Filters: in.Filters}, in.Before, in.Limit)
	return nil, visitsOut{Visits: vs}, friendly(err)
}

type visitIn struct {
	Service string `json:"service" jsonschema:"project/service of an app with analytics on"`
	Visit   string `json:"visit" jsonschema:"a visit id, from list_visits"`
}

func (t *tools) getVisit(ctx context.Context, _ *mcp.CallToolRequest, in visitIn) (*mcp.CallToolResult, analytics.VisitDetail, error) {
	if err := core.Authenticated(ctx); err != nil {
		return nil, analytics.VisitDetail{}, err
	}
	svc, err := t.service(ctx, in.Service, core.PermView)
	if err != nil {
		return nil, analytics.VisitDetail{}, friendly(err)
	}
	d, err := t.c.ServiceVisit(ctx, svc.ID, in.Visit)
	return nil, d, friendly(err)
}
