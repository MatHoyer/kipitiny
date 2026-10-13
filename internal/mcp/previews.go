package mcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/store"
)

type listPreviewsIn struct {
	Project string `json:"project" jsonschema:"the project whose previews to list"`
}

type listPreviewsOut struct {
	Previews []core.PreviewView `json:"previews"`
}

func (t *tools) listPreviews(ctx context.Context, _ *mcp.CallToolRequest, in listPreviewsIn) (*mcp.CallToolResult, listPreviewsOut, error) {
	if err := core.Require(ctx, store.ScopeRead); err != nil {
		return nil, listPreviewsOut{}, err
	}
	p, err := t.project(ctx, in.Project, core.PermView)
	if err != nil {
		return nil, listPreviewsOut{}, friendly(err)
	}
	ps, err := t.c.ListPreviews(ctx, p.ID)
	return nil, listPreviewsOut{Previews: ps}, friendly(err)
}

type deployPreviewIn struct {
	Project string            `json:"project" jsonschema:"the project previewed"`
	Name    string            `json:"name" jsonschema:"the preview's name, e.g. pr-12 (lowercase letters, digits, dashes)"`
	Tags    map[string]string `json:"tags,omitempty" jsonschema:"image tag (or sha256: digest) to deploy, by service; the others keep the project's image"`
	Branch  string            `json:"branch,omitempty" jsonschema:"source branch: a git-linked project's compose file is read there"`
	Commit  string            `json:"commit,omitempty" jsonschema:"source commit, recorded on the deployments"`
	Env     map[string]string `json:"env,omitempty" jsonschema:"project variables for this preview only, plain text (no references)"`
	TTL     string            `json:"ttl,omitempty" jsonschema:"close the preview after this long unused, as a duration (168h); default from the project's preview settings, else 7 days"`
}

func (t *tools) deployPreview(ctx context.Context, _ *mcp.CallToolRequest, in deployPreviewIn) (*mcp.CallToolResult, core.PreviewView, error) {
	out, err := mutate(ctx, t.c, store.ScopeDeploy, "deploy_preview", in.Project+"/"+in.Name, func() (core.PreviewView, error) {
		p, err := t.project(ctx, in.Project, core.PermDeploy)
		if err != nil {
			return core.PreviewView{}, err
		}
		opts := core.PreviewOptions{Tags: in.Tags, Branch: in.Branch, Commit: in.Commit, Env: in.Env}
		if in.TTL != "" {
			d, err := time.ParseDuration(in.TTL)
			if err != nil {
				return core.PreviewView{}, err
			}
			opts.TTLSeconds = int(d.Seconds())
		}
		return t.c.DeployPreview(ctx, p.ID, in.Name, opts)
	})
	return nil, out, err
}

type closePreviewIn struct {
	Project string `json:"project" jsonschema:"the project previewed"`
	Name    string `json:"name" jsonschema:"the preview to close"`
}

type closePreviewOut struct {
	Closed string `json:"closed"`
}

func (t *tools) closePreview(ctx context.Context, _ *mcp.CallToolRequest, in closePreviewIn) (*mcp.CallToolResult, closePreviewOut, error) {
	out, err := mutate(ctx, t.c, store.ScopeDeploy, "close_preview", in.Project+"/"+in.Name, func() (closePreviewOut, error) {
		p, err := t.project(ctx, in.Project, core.PermDeploy)
		if err != nil {
			return closePreviewOut{}, err
		}
		return closePreviewOut{Closed: in.Name}, t.c.ClosePreview(ctx, p.ID, in.Name)
	})
	return nil, out, err
}
