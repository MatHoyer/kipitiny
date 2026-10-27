package mcp

import (
	"context"
	"maps"
	"slices"
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
	if err := core.Authenticated(ctx); err != nil {
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
	Seed    bool              `json:"seed,omitempty" jsonschema:"fill the databases a new preview gets with the project's latest backups (production data; the preview settings must allow it)"`
}

func (t *tools) deployPreview(ctx context.Context, _ *mcp.CallToolRequest, in deployPreviewIn) (*mcp.CallToolResult, core.PreviewView, error) {
	out, err := mutate(ctx, t.c, "deploy_preview", in.Project+"/"+in.Name, func() (core.PreviewView, error) {
		p, err := t.project(ctx, in.Project, core.PermDeploy)
		if err != nil {
			return core.PreviewView{}, err
		}
		opts := core.PreviewOptions{Tags: in.Tags, Branch: in.Branch, Commit: in.Commit, Env: in.Env, Seed: in.Seed}
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
	out, err := mutate(ctx, t.c, "close_preview", in.Project+"/"+in.Name, func() (closePreviewOut, error) {
		p, err := t.project(ctx, in.Project, core.PermDeploy)
		if err != nil {
			return closePreviewOut{}, err
		}
		return closePreviewOut{Closed: in.Name}, t.c.ClosePreview(ctx, p.ID, in.Name)
	})
	return nil, out, err
}

type getPreviewSettingsIn struct {
	Project string `json:"project" jsonschema:"the project whose preview settings to read"`
}

func (t *tools) getPreviewSettings(ctx context.Context, _ *mcp.CallToolRequest, in getPreviewSettingsIn) (*mcp.CallToolResult, store.PreviewSettings, error) {
	if err := core.Authenticated(ctx); err != nil {
		return nil, store.PreviewSettings{}, err
	}
	p, err := t.project(ctx, in.Project, core.PermView)
	if err != nil {
		return nil, store.PreviewSettings{}, friendly(err)
	}
	ps, err := t.c.GetPreviewSettings(ctx, p.ID)
	return nil, ps, friendly(err)
}

type setPreviewSettingsIn struct {
	Project    string            `json:"project" jsonschema:"the project whose previews these settings apply to"`
	BaseDomain *string           `json:"base_domain,omitempty" jsonschema:"domain hosting the previews' apps as <preview>-<service>.<base_domain>; empty removes it"`
	Set        map[string]string `json:"set,omitempty" jsonschema:"plain preview variables to add or overwrite (readable), replacing the project's of the same name"`
	Secrets    map[string]string `json:"secrets,omitempty" jsonschema:"preview secrets to add or overwrite (write-only)"`
	Unset      []string          `json:"unset,omitempty" jsonschema:"preview variables or secrets to remove"`
	Inherit    *[]string         `json:"inherit,omitempty" jsonschema:"the project's secrets previews may still use (replaces the list); the others are left out"`
	TTL        *string           `json:"ttl,omitempty" jsonschema:"close a preview after this long unused, as a duration (168h); 0 for the default of 7 days"`
	SleepAfter *string           `json:"sleep_after,omitempty" jsonschema:"idle time before a preview sleeps, as a duration (30m; 5 minutes to 30 days); 0 for the default"`
	Seed       *string           `json:"seed,omitempty" jsonschema:"what deploy_preview --seed fills new previews' databases with: empty (off), anonymized (the latest backups anonymized with each database's rules) or raw (production data as is)"`
}

// setPreviewSettings changes only the fields given, unlike the API's
// replace, so an agent can't drop secrets it can't read.
func (t *tools) setPreviewSettings(ctx context.Context, _ *mcp.CallToolRequest, in setPreviewSettingsIn) (*mcp.CallToolResult, store.PreviewSettings, error) {
	out, err := mutate(ctx, t.c, "set_preview_settings", in.Project, func() (store.PreviewSettings, error) {
		p, err := t.project(ctx, in.Project, core.PermEnv)
		if err != nil {
			return store.PreviewSettings{}, err
		}
		ps, err := t.c.GetPreviewSettings(ctx, p.ID) // secrets masked: unchanged ones are kept
		if err != nil {
			return store.PreviewSettings{}, err
		}
		next := core.PreviewSettingsInput{BaseDomain: ps.BaseDomain, Env: ps.Env, Inherit: ps.Inherit,
			TTLSeconds: ps.TTLSeconds, SleepAfterSeconds: ps.SleepAfterSeconds, Seed: ps.SeedMode}
		next.Secrets = slices.DeleteFunc(ps.Secrets, func(k string) bool { _, set := in.Set[k]; return set })
		maps.Copy(next.Env, in.Set)
		maps.Copy(next.Env, in.Secrets)
		next.Secrets = append(next.Secrets, slices.Collect(maps.Keys(in.Secrets))...)
		for _, k := range in.Unset {
			delete(next.Env, k)
		}
		if in.BaseDomain != nil {
			next.BaseDomain = *in.BaseDomain
		}
		if in.Inherit != nil {
			next.Inherit = *in.Inherit
		}
		if in.TTL != nil {
			if next.TTLSeconds, err = seconds(*in.TTL); err != nil {
				return store.PreviewSettings{}, err
			}
		}
		if in.SleepAfter != nil {
			if next.SleepAfterSeconds, err = seconds(*in.SleepAfter); err != nil {
				return store.PreviewSettings{}, err
			}
		}
		if in.Seed != nil {
			next.Seed = store.SeedMode(*in.Seed)
		}
		return t.c.SetPreviewSettings(ctx, p.ID, next)
	})
	return nil, out, err
}

// seconds parses a duration given to a tool; "0" is zero.
func seconds(s string) (int, error) {
	if s == "0" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	return int(d.Seconds()), err
}
