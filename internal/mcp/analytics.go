package mcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MatHoyer/kipitiny/internal/core"
)

type analyticsIn struct {
	Service string `json:"service" jsonschema:"project/service of an app with analytics on"`
	Days    int    `json:"days,omitempty" jsonschema:"how many days back, default 7, at most 730"`
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
	out, err := t.c.ServiceAnalytics(ctx, svc.ID, core.AnalyticsQuery{From: now.AddDate(0, 0, -days), To: now, Interval: 86400})
	return nil, out, friendly(err)
}
