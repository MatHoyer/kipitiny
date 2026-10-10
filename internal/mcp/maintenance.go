package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/store"
)

type setMaintenanceIn struct {
	Service  string    `json:"service" jsonschema:"the app as project/service, or its ID"`
	Enabled  *bool     `json:"enabled,omitempty" jsonschema:"maintenance mode: every visitor gets the page while the replicas keep running; omitted keeps it"`
	AllowIPs *[]string `json:"allow_ips,omitempty" jsonschema:"IPs or CIDR ranges that still reach the app in maintenance mode; omitted keeps them, empty removes them"`
	Title    *string   `json:"title,omitempty" jsonschema:"the default page's heading; empty restores \"We'll be back soon\"; omitted keeps it"`
	Message  *string   `json:"message,omitempty" jsonschema:"the default page's text; empty restores the default; omitted keeps it"`
	HTML     *string   `json:"html,omitempty" jsonschema:"a whole HTML document served instead of the default page (max 64 KiB); empty restores the default page; omitted keeps it"`
}

func (t *tools) setMaintenance(ctx context.Context, _ *mcp.CallToolRequest, in setMaintenanceIn) (*mcp.CallToolResult, store.Maintenance, error) {
	out, err := mutate(ctx, t.c, store.ScopeAdmin, "set_maintenance", in.Service, func() (store.Maintenance, error) {
		svc, err := t.c.ResolveService(ctx, in.Service)
		if err != nil {
			return store.Maintenance{}, err
		}
		m := svc.Maintenance
		v := core.MaintenanceInput{Enabled: m.Enabled, AllowIPs: m.AllowIPs, Title: m.Title, Message: m.Message, HTML: m.HTML}
		if in.Enabled != nil {
			v.Enabled = *in.Enabled
		}
		if in.AllowIPs != nil {
			v.AllowIPs = *in.AllowIPs
		}
		if in.Title != nil {
			v.Title = *in.Title
		}
		if in.Message != nil {
			v.Message = *in.Message
		}
		if in.HTML != nil {
			v.HTML = *in.HTML
		}
		sv, err := t.c.SetMaintenance(ctx, svc.ID, v)
		return sv.Maintenance, err
	})
	return nil, out, err
}
