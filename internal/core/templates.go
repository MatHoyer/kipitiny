package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/MatHoyer/kipitiny/internal/store"
	"github.com/MatHoyer/kipitiny/internal/templates"
)

// TemplateInstall installs a template into a project: an existing one
// (ProjectID) or one it creates (NewProject).
type TemplateInstall struct {
	ProjectID  string      `json:"projectId,omitempty"`
	NewProject *NewProject `json:"newProject,omitempty"`
	// Values are the template's inputs, by name; generated ones aren't given.
	Values map[string]string `json:"values"`
	// DryRun returns the plan without creating anything.
	DryRun bool `json:"dryRun"`
}

// NewProject is a project to create on a server (this one when empty).
type NewProject struct {
	Name     string `json:"name"`
	ServerID string `json:"serverId,omitempty"`
}

// TemplateResult is what installing a template did, or would do.
type TemplateResult struct {
	ProjectID string      `json:"projectId,omitempty"`
	Plan      ComposePlan `json:"plan"`
}

func (c *Core) ListTemplates() []templates.Template { return templates.List() }

// InstallTemplate applies a template's compose file with the user's inputs
// as its .env, then deploys what it created. Nothing is pruned: a template
// only adds to a project. A new project is created only once the file is
// known to apply.
func (c *Core) InstallTemplate(ctx context.Context, id string, in TemplateInstall) (TemplateResult, error) {
	t, ok := templates.Get(id)
	if !ok {
		return TemplateResult{}, fmt.Errorf("%w: template %s", store.ErrNotFound, id)
	}
	if (in.ProjectID == "") == (in.NewProject == nil) {
		return TemplateResult{}, fmt.Errorf("%w: give either a project or a new project", ErrInvalid)
	}
	env, err := t.Render(in.Values, randomToken)
	if err != nil {
		return TemplateResult{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	opts := ApplyOptions{Env: env, Deploy: true}
	data := []byte(t.Compose)
	if in.ProjectID != "" {
		// Only adds: a service of the template's name already there would
		// be changed by the apply.
		plan, err := c.ApplyCompose(ctx, in.ProjectID, data, ApplyOptions{Env: env, DryRun: true})
		if err != nil {
			return TemplateResult{ProjectID: in.ProjectID, Plan: plan}, err
		}
		if taken := append(plan.Unchanged, serviceNames(plan.Update)...); len(taken) > 0 {
			return TemplateResult{}, fmt.Errorf("%w: the project already has %s", ErrInvalid, strings.Join(taken, ", "))
		}
		if in.DryRun {
			return TemplateResult{ProjectID: in.ProjectID, Plan: plan}, nil
		}
		plan, err = c.ApplyCompose(ctx, in.ProjectID, data, opts)
		return TemplateResult{ProjectID: in.ProjectID, Plan: plan}, err
	}

	np := *in.NewProject
	if !nameRe.MatchString(np.Name) {
		return TemplateResult{}, fmt.Errorf("%w: name must be lowercase letters, digits and dashes (max 40)", ErrInvalid)
	}
	if np.ServerID == "" {
		np.ServerID = store.LocalServerID
	}
	if _, err := c.store.GetServer(ctx, np.ServerID); err != nil {
		return TemplateResult{}, fmt.Errorf("%w: unknown server", ErrInvalid)
	}
	a, err := c.planCompose(ctx, store.Project{Name: np.Name, ServerID: np.ServerID}, nil, data, ApplyOptions{Env: env, DryRun: true}, false)
	if a == nil || err != nil || in.DryRun {
		var plan ComposePlan
		if a != nil {
			plan = a.plan
		}
		return TemplateResult{Plan: plan}, err
	}
	p, err := c.CreateProject(ctx, np.Name, np.ServerID)
	if err != nil {
		return TemplateResult{}, err
	}
	plan, err := c.ApplyCompose(ctx, p.ID, data, opts)
	return TemplateResult{ProjectID: p.ID, Plan: plan}, err
}

func serviceNames(cs []ServiceChange) []string {
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = c.Name
	}
	return names
}
