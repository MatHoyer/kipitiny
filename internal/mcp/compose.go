package mcp

import (
	"context"
	"errors"
	"maps"
	"slices"

	"github.com/kipit-labs/kipitiny/internal/usecase/deploy"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type setProjectEnvIn struct {
	Project string            `json:"project" jsonschema:"project name"`
	Set     map[string]string `json:"set,omitempty" jsonschema:"plain variables to add or overwrite (readable)"`
	Secrets map[string]string `json:"secrets,omitempty" jsonschema:"secrets to add or overwrite (write-only)"`
	Unset   []string          `json:"unset,omitempty" jsonschema:"variables or secrets to remove"`
}

type setProjectEnvOut struct {
	// Variables and Secrets list the project's names after the change.
	Variables []string `json:"variables"`
	Secrets   []string `json:"secrets"`
}

func (t *tools) setProjectEnv(ctx context.Context, _ *mcp.CallToolRequest, in setProjectEnvIn) (*mcp.CallToolResult, setProjectEnvOut, error) {
	out, err := mutate(ctx, t.c, "set_project_env", in.Project, func() (setProjectEnvOut, error) {
		project, err := t.project(ctx, in.Project, identity.PermEnv)
		if err != nil {
			return setProjectEnvOut{}, err
		}
		env := project.Env // secrets masked: unchanged ones are kept
		secrets := slices.DeleteFunc(project.Secrets, func(k string) bool { _, set := in.Set[k]; return set })
		maps.Copy(env, in.Set)
		maps.Copy(env, in.Secrets)
		secrets = append(secrets, slices.Collect(maps.Keys(in.Secrets))...)
		for _, k := range in.Unset {
			delete(env, k)
		}
		if project, err = t.c.SetProjectEnv(ctx, project.ID, env, secrets); err != nil {
			return setProjectEnvOut{}, err
		}
		out := setProjectEnvOut{Variables: []string{}, Secrets: project.Secrets}
		for k := range project.Env {
			if !slices.Contains(project.Secrets, k) {
				out.Variables = append(out.Variables, k)
			}
		}
		slices.Sort(out.Variables)
		return out, nil
	})
	return nil, out, err
}

type getProjectComposeIn struct {
	Project string `json:"project" jsonschema:"the project name"`
	Service string `json:"service,omitempty" jsonschema:"only this service of the project"`
}

type getProjectComposeOut struct {
	Compose string `json:"compose"`
}

func (t *tools) getProjectCompose(ctx context.Context, _ *mcp.CallToolRequest, in getProjectComposeIn) (*mcp.CallToolResult, getProjectComposeOut, error) {
	if err := identity.Authenticated(ctx); err != nil {
		return nil, getProjectComposeOut{}, err
	}
	project, err := t.project(ctx, in.Project, identity.PermView)
	if err != nil {
		return nil, getProjectComposeOut{}, friendly(err)
	}
	out, err := t.c.ExportCompose(ctx, project.ID, deploy.ExportOptions{Service: in.Service})
	if err != nil {
		return nil, getProjectComposeOut{}, friendly(err)
	}
	return nil, getProjectComposeOut{Compose: string(out.Compose)}, nil
}

type applyProjectComposeIn struct {
	Project string `json:"project" jsonschema:"the project name"`
	Compose string `json:"compose" jsonschema:"the compose file"`
	DryRun  bool   `json:"dry_run,omitempty" jsonschema:"only return what would change"`
	Prune   bool   `json:"prune,omitempty" jsonschema:"delete the apps the file doesn't list"`
	Confirm string `json:"confirm,omitempty" jsonschema:"the project name, required with prune"`
}

func (t *tools) applyProjectCompose(ctx context.Context, _ *mcp.CallToolRequest, in applyProjectComposeIn) (*mcp.CallToolResult, deploy.ComposePlan, error) {
	out, err := mutate(ctx, t.c, "apply_project_compose", in.Project, func() (deploy.ComposePlan, error) {
		project, err := t.project(ctx, in.Project, identity.PermSettings)
		if err != nil {
			return deploy.ComposePlan{}, err
		}
		if in.Prune && !in.DryRun && in.Confirm != project.Name {
			return deploy.ComposePlan{}, errors.New("prune deletes apps and their data: confirm must repeat the project name")
		}
		return t.c.ApplyCompose(ctx, project.ID, []byte(in.Compose), deploy.ApplyOptions{DryRun: in.DryRun, Prune: in.Prune, Deploy: true})
	})
	return nil, out, err
}
