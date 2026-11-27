package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
	"github.com/kipit-labs/kipitiny/internal/usecase/monitoring"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kipit-labs/kipitiny/internal/core"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

type serviceArg struct {
	Service string `json:"service" jsonschema:"the service as project/service, or its ID"`
}

type ServiceSummary struct {
	Project string   `json:"project"`
	Service string   `json:"service"`
	Kind    string   `json:"kind"`
	Image   string   `json:"image"`
	Domain  string   `json:"domain,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	Status  string   `json:"status"`
}

type listServicesOut struct {
	Services []ServiceSummary `json:"services"`
}

func (t *tools) listServices(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listServicesOut, error) {
	if err := identity.Authenticated(ctx); err != nil {
		return nil, listServicesOut{}, err
	}
	projects, err := t.c.ListProjects(ctx)
	if err != nil {
		return nil, listServicesOut{}, err
	}
	out := listServicesOut{Services: []ServiceSummary{}}
	for _, p := range projects {
		svcs, err := t.c.ListServices(ctx, p.ID)
		if err != nil {
			return nil, listServicesOut{}, err
		}
		for _, s := range svcs {
			out.Services = append(out.Services, ServiceSummary{
				Project: p.Name, Service: s.Name, Kind: string(s.Kind), Image: s.Image,
				Domain: s.Domain, Tags: s.Tags, Status: status(s),
			})
		}
	}
	return nil, out, nil
}

// status summarises a service's active containers.
func status(s core.ServiceView) string {
	if s.Stopped {
		return "stopped"
	}
	running, total := 0, 0
	for _, ct := range s.Containers {
		if ct.Retired {
			continue
		}
		total++
		if ct.State == "running" && ct.Health != "unhealthy" {
			running++
		}
	}
	switch {
	case total == 0:
		return "not deployed"
	case running == total:
		return fmt.Sprintf("running (%d/%d)", running, total)
	case running == 0:
		return "down"
	}
	return fmt.Sprintf("degraded (%d/%d healthy)", running, total)
}

type appStatusOut struct {
	Service     core.ServiceView    `json:"service"`
	Status      string              `json:"status"`
	Deployments []domain.Deployment `json:"recentDeployments"`
	// Usage is the current CPU, memory and network use; nil when not running.
	Usage *core.Usage `json:"usage,omitempty"`
	// Uptime is the uptime check and its results, when the service has one.
	Uptime *monitoring.UptimeView `json:"uptime,omitempty"`
}

func (t *tools) getAppStatus(ctx context.Context, _ *mcp.CallToolRequest, in serviceArg) (*mcp.CallToolResult, appStatusOut, error) {
	if err := identity.Authenticated(ctx); err != nil {
		return nil, appStatusOut{}, err
	}
	svc, err := t.service(ctx, in.Service, identity.PermView)
	if err != nil {
		return nil, appStatusOut{}, friendly(err)
	}
	view, err := t.c.GetService(ctx, svc.ID)
	if err != nil {
		return nil, appStatusOut{}, err
	}
	deps, err := t.c.ListDeployments(ctx, svc.ID)
	if err != nil {
		return nil, appStatusOut{}, err
	}
	stats, err := t.c.ServiceStats(ctx, svc.ID)
	if err != nil {
		return nil, appStatusOut{}, err
	}
	out := appStatusOut{Service: view, Status: status(view), Deployments: deps[:min(len(deps), 5)], Usage: stats.Current}
	up, err := t.c.Uptime(ctx, svc.ID)
	if err != nil {
		return nil, appStatusOut{}, err
	}
	if up.Check != nil {
		out.Uptime = &up
	}
	return nil, out, nil
}

type getLogsIn struct {
	Service string `json:"service" jsonschema:"the service as project/service, or its ID"`
	Lines   int    `json:"lines,omitempty" jsonschema:"lines per container, default 100, max 500"`
	Filter  string `json:"filter,omitempty" jsonschema:"only lines containing this text (case-insensitive)"`
}

type getLogsOut struct {
	Lines []core.LogLine `json:"lines"`
}

func (t *tools) getLogs(ctx context.Context, _ *mcp.CallToolRequest, in getLogsIn) (*mcp.CallToolResult, getLogsOut, error) {
	if err := identity.Authenticated(ctx); err != nil {
		return nil, getLogsOut{}, err
	}
	svc, err := t.service(ctx, in.Service, identity.PermView)
	if err != nil {
		return nil, getLogsOut{}, friendly(err)
	}
	n := in.Lines
	if n <= 0 {
		n = 100
	}
	lines, err := t.c.RecentLogs(ctx, svc.ID, min(n, 500))
	if err != nil {
		return nil, getLogsOut{}, err
	}
	out := getLogsOut{Lines: []core.LogLine{}}
	needle := strings.ToLower(in.Filter)
	for _, l := range lines {
		if needle == "" || strings.Contains(strings.ToLower(l.Text), needle) {
			out.Lines = append(out.Lines, l)
		}
	}
	return nil, out, nil
}
