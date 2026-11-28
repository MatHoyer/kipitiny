package mcp

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/app"
	"github.com/kipit-labs/kipitiny/internal/usecase/projects"
	"github.com/kipit-labs/kipitiny/internal/usecase/servers"
	"github.com/kipit-labs/kipitiny/internal/usecase/templates"
)

type listTemplatesOut struct {
	Templates []templates.Template `json:"templates"`
}

func (t *tools) listTemplates(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listTemplatesOut, error) {
	if err := identity.Authenticated(ctx); err != nil {
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

func (t *tools) installTemplate(ctx context.Context, _ *mcp.CallToolRequest, in installTemplateIn) (*mcp.CallToolResult, projects.TemplateResult, error) {
	out, err := mutate(ctx, t.c, "install_template", in.Project, func() (projects.TemplateResult, error) {
		req := projects.TemplateInstall{Values: in.Values, DryRun: in.DryRun}
		p, err := t.findProject(ctx, in.Project)
		switch {
		case err == nil:
			if in.Server != "" {
				return projects.TemplateResult{}, fmt.Errorf("%w: project %s already exists; server is only for a new one", app.ErrInvalid, in.Project)
			}
			req.ProjectID = p.ID
		case errors.Is(err, domain.ErrNotFound):
			req.NewProject = &projects.NewProject{Name: in.Project}
			if in.Server != "" {
				svs, err := t.c.ListServers(ctx)
				if err != nil {
					return projects.TemplateResult{}, err
				}
				i := slices.IndexFunc(svs, func(sv servers.ServerView) bool { return sv.Name == in.Server })
				if i < 0 {
					return projects.TemplateResult{}, fmt.Errorf("%w: server %s", domain.ErrNotFound, in.Server)
				}
				req.NewProject.ServerID = svs[i].ID
			}
		default:
			return projects.TemplateResult{}, err
		}
		return t.c.InstallTemplate(ctx, in.Template, req)
	})
	return nil, out, err
}
