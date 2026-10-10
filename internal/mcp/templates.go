package mcp

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/store"
	"github.com/MatHoyer/kipitiny/internal/templates"
)

type listTemplatesOut struct {
	Templates []templates.Template `json:"templates"`
}

func (t *tools) listTemplates(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listTemplatesOut, error) {
	if err := core.Require(ctx, store.ScopeRead); err != nil {
		return nil, listTemplatesOut{}, err
	}
	return nil, listTemplatesOut{Templates: t.c.ListTemplates()}, nil
}

type installTemplateIn struct {
	Template string            `json:"template" jsonschema:"the template's id, from list_templates"`
	Project  string            `json:"project" jsonschema:"the project to install into; created if missing"`
	Server   string            `json:"server,omitempty" jsonschema:"the server's name for a new project; default: this one"`
	Values   map[string]string `json:"values,omitempty" jsonschema:"the template's inputs by name; generated ones are not given"`
	DryRun   bool              `json:"dry_run,omitempty" jsonschema:"only return what would be created"`
}

func (t *tools) installTemplate(ctx context.Context, _ *mcp.CallToolRequest, in installTemplateIn) (*mcp.CallToolResult, core.TemplateResult, error) {
	out, err := mutate(ctx, t.c, store.ScopeAdmin, "install_template", in.Project, func() (core.TemplateResult, error) {
		req := core.TemplateInstall{Values: in.Values, DryRun: in.DryRun}
		p, err := t.findProject(ctx, in.Project)
		switch {
		case err == nil:
			if in.Server != "" {
				return core.TemplateResult{}, fmt.Errorf("%w: project %s already exists; server is only for a new one", core.ErrInvalid, in.Project)
			}
			req.ProjectID = p.ID
		case errors.Is(err, store.ErrNotFound):
			req.NewProject = &core.NewProject{Name: in.Project}
			if in.Server != "" {
				servers, err := t.c.ListServers(ctx)
				if err != nil {
					return core.TemplateResult{}, err
				}
				i := slices.IndexFunc(servers, func(sv core.ServerView) bool { return sv.Name == in.Server })
				if i < 0 {
					return core.TemplateResult{}, fmt.Errorf("%w: server %s", store.ErrNotFound, in.Server)
				}
				req.NewProject.ServerID = servers[i].ID
			}
		default:
			return core.TemplateResult{}, err
		}
		return t.c.InstallTemplate(ctx, in.Template, req)
	})
	return nil, out, err
}
