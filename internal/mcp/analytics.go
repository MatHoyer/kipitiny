package mcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MatHoyer/kipitiny/internal/analytics"
	"github.com/MatHoyer/kipitiny/internal/core"
)

type analyticsIn struct {
	Service string   `json:"service" jsonschema:"project/service of an app with analytics on"`
	Days    int      `json:"days,omitempty" jsonschema:"how many days back, default 7, at most 730"`
	Filters []string `json:"filters,omitempty" jsonschema:"narrow to breakdown values, as <breakdown>:<value>, e.g. countries:FR, pages:/pricing, referrers:google.com, events:signup, entries:/"`
}

func (t *tools) getAnalytics(ctx context.Context, _ *mcp.CallToolRequest, in analyticsIn) (*mcp.CallToolResult, core.AnalyticsView, error) {
	if err := core.Authenticated(ctx); err != nil {
		return nil, core.AnalyticsView{}, err
	}
	svc, err := t.service(ctx, in.Service, core.PermView)
	if err != nil {
		return nil, core.AnalyticsView{}, friendly(err)
	}
	days := in.Days
	if days <= 0 {
		days = 7
	}
	days = min(days, 730)
	now := time.Now()
	out, err := t.c.ServiceAnalytics(ctx, svc.ID, core.AnalyticsQuery{From: now.AddDate(0, 0, -days), To: now, Interval: 86400, Filters: in.Filters})
	return nil, out, friendly(err)
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
