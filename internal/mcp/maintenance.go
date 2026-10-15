package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/store"
)

type setMaintenanceIn struct {
	Project  string    `json:"project" jsonschema:"the project's name or ID"`
	Enabled  *bool     `json:"enabled,omitempty" jsonschema:"maintenance mode: every visitor of the project's apps gets the page while the replicas keep running; omitted keeps it"`
	AllowIPs *[]string `json:"allow_ips,omitempty" jsonschema:"IPs or CIDR ranges that still reach the apps in maintenance mode; omitted keeps them, empty removes them"`
	Title    *string   `json:"title,omitempty" jsonschema:"the default page's heading; empty restores \"We'll be back soon\"; omitted keeps it"`
	Message  *string   `json:"message,omitempty" jsonschema:"the default page's text; empty restores the default; omitted keeps it"`
	HTML     *string   `json:"html,omitempty" jsonschema:"a whole HTML document served instead of the default page (max 64 KiB); empty restores the default page; omitted keeps it"`
	// The starting page of a project waking from sleep.
	StartingTitle   *string `json:"starting_title,omitempty" jsonschema:"the default starting page's heading (shown while the project wakes from sleep); empty restores \"Starting…\"; omitted keeps it"`
	StartingMessage *string `json:"starting_message,omitempty" jsonschema:"the default starting page's text; empty restores the default; omitted keeps it"`
	StartingHTML    *string `json:"starting_html,omitempty" jsonschema:"a whole HTML document served while the project wakes (max 64 KiB; the manager makes it reload every 3 s); empty restores the default; omitted keeps it"`
}

func (t *tools) setMaintenance(ctx context.Context, _ *mcp.CallToolRequest, in setMaintenanceIn) (*mcp.CallToolResult, store.Maintenance, error) {
	out, err := mutate(ctx, t.c, "set_maintenance", in.Project, func() (store.Maintenance, error) {
		p, err := t.project(ctx, in.Project, core.PermDeploy)
		if err != nil {
			return store.Maintenance{}, err
		}
		m := p.Maintenance
		v := core.MaintenanceInput{Enabled: m.Enabled, AllowIPs: m.AllowIPs, Title: m.Title, Message: m.Message, HTML: m.HTML, Starting: m.Starting}
		for dst, src := range map[*string]*string{&v.Starting.Title: in.StartingTitle, &v.Starting.Message: in.StartingMessage, &v.Starting.HTML: in.StartingHTML} {
			if src != nil {
				*dst = *src
			}
		}
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
		p, err = t.c.SetMaintenance(ctx, p.ID, v)
		return p.Maintenance, err
	})
	return nil, out, err
}
