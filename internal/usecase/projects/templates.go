package projects

import (
	"context"
	"crypto/rand"
	"fmt"
	"slices"
	"strings"

	"github.com/kipit-labs/kipitiny/internal/usecase/deploy"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/templates"
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
	ProjectID string             `json:"projectId,omitempty"`
	Plan      deploy.ComposePlan `json:"plan"`
}

func (c *Projects) ListTemplates() []templates.Template { return templates.List() }

// TemplateLogos are the service logos templates bring, for the UI.
func (c *Projects) TemplateLogos() []templates.Logo { return templates.Logos() }

// InstallTemplate applies a template's compose file with the user's inputs
// as its .env, then deploys what it created. Nothing is pruned: a template
// only adds to a project. A new project is created only once the file is
// known to apply.
func (c *Projects) InstallTemplate(ctx context.Context, id string, in TemplateInstall) (TemplateResult, error) {
	t, ok := templates.Get(id)
	if !ok {
		return TemplateResult{}, fmt.Errorf("%w: template %s", domain.ErrNotFound, id)
	}
	if (in.ProjectID == "") == (in.NewProject == nil) {
		return TemplateResult{}, fmt.Errorf("%w: give either a project or a new project", ErrInvalid)
	}
	if in.ProjectID != "" {
		if err := identity.Check(ctx, in.ProjectID, identity.PermSettings); err != nil {
			return TemplateResult{}, err
		}
	} else if err := identity.CheckFeature(ctx, identity.FeatureCreateProjects); err != nil {
		return TemplateResult{}, err
	}
	env, err := t.Render(in.Values, func(n int) []byte {
		b := make([]byte, n)
		_, _ = rand.Read(b) // never fails since Go 1.24
		return b
	})
	if err != nil {
		return TemplateResult{}, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	opts := deploy.ApplyOptions{Env: env, Deploy: true}
	data := []byte(t.Compose)
	if in.ProjectID != "" {
		// Only adds: a service of the template's name already there would
		// be changed by the apply.
		plan, err := c.mgr.ApplyCompose(ctx, in.ProjectID, data, deploy.ApplyOptions{Env: env, DryRun: true})
		if err != nil {
			return TemplateResult{ProjectID: in.ProjectID, Plan: plan}, err
		}
		if taken := slices.Concat(plan.Unchanged, serviceNames(plan.Update)); len(taken) > 0 {
			return TemplateResult{}, fmt.Errorf("%w: the project already has %s", ErrInvalid, strings.Join(taken, ", "))
		}
		if in.DryRun {
			return TemplateResult{ProjectID: in.ProjectID, Plan: plan}, nil
		}
		plan, err = c.mgr.ApplyCompose(ctx, in.ProjectID, data, opts)
		return TemplateResult{ProjectID: in.ProjectID, Plan: plan}, err
	}

	np := *in.NewProject
	if !domain.ValidName(np.Name) {
		return TemplateResult{}, fmt.Errorf("%w: name must be lowercase letters, digits and dashes (max 40)", ErrInvalid)
	}
	if np.ServerID == "" {
		np.ServerID = domain.LocalServerID
	}
	if _, err := c.store.GetServer(ctx, np.ServerID); err != nil {
		return TemplateResult{}, fmt.Errorf("%w: unknown server", ErrInvalid)
	}
	plan, err := c.mgr.PlanCompose(ctx, domain.Project{Name: np.Name, ServerID: np.ServerID}, data, deploy.ApplyOptions{Env: env, DryRun: true})
	if err != nil || in.DryRun {
		return TemplateResult{Plan: plan}, err
	}
	p, err := c.CreateProject(ctx, np.Name, np.ServerID)
	if err != nil {
		return TemplateResult{}, err
	}
	plan, err = c.mgr.ApplyCompose(ctx, p.ID, data, opts)
	return TemplateResult{ProjectID: p.ID, Plan: plan}, err
}

func serviceNames(cs []deploy.ServiceChange) []string {
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = c.Name
	}
	return names
}
