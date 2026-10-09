package mcp

import (
	"context"
	"fmt"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/gitprovider"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// The manager as a whole: git repositories it can read, the network map,
// its version and resource use, and the audit log.

type GitRepos struct {
	Provider string             `json:"provider"`
	Repos    []gitprovider.Repo `json:"repos"`
	// Error is why this provider's repositories could not be listed.
	Error string `json:"error,omitempty"`
}

type listGitReposIn struct {
	Provider string `json:"provider,omitempty" jsonschema:"a git provider's name; default every connected one"`
}

type listGitReposOut struct {
	Providers []GitRepos `json:"providers"`
}

func (t *tools) listGitRepos(ctx context.Context, _ *mcp.CallToolRequest, in listGitReposIn) (*mcp.CallToolResult, listGitReposOut, error) {
	if err := core.Require(ctx, store.ScopeRead); err != nil {
		return nil, listGitReposOut{}, err
	}
	ps, err := t.c.ListGitProviders(ctx)
	if err != nil {
		return nil, listGitReposOut{}, err
	}
	out := listGitReposOut{Providers: []GitRepos{}}
	for _, p := range ps {
		if (in.Provider != "" && p.Name != in.Provider) || (in.Provider == "" && !p.Connected) {
			continue
		}
		g := GitRepos{Provider: p.Name, Repos: []gitprovider.Repo{}}
		if repos, err := t.c.GitProviderRepos(ctx, p.ID); err != nil {
			g.Error = err.Error()
		} else {
			g.Repos = repos
		}
		out.Providers = append(out.Providers, g)
	}
	if in.Provider != "" && len(out.Providers) == 0 {
		return nil, listGitReposOut{}, fmt.Errorf("not found: git provider %s", in.Provider)
	}
	return nil, out, nil
}

type listGitBranchesIn struct {
	Provider string `json:"provider" jsonschema:"the git provider's name"`
	Repo     string `json:"repo" jsonschema:"the repository's full name (owner/name), see list_git_repos"`
}

type listGitBranchesOut struct {
	Branches []string `json:"branches"`
}

func (t *tools) listGitBranches(ctx context.Context, _ *mcp.CallToolRequest, in listGitBranchesIn) (*mcp.CallToolResult, listGitBranchesOut, error) {
	if err := core.Require(ctx, store.ScopeRead); err != nil {
		return nil, listGitBranchesOut{}, err
	}
	ps, err := t.c.ListGitProviders(ctx)
	if err != nil {
		return nil, listGitBranchesOut{}, err
	}
	i := slices.IndexFunc(ps, func(p core.GitProviderView) bool { return p.Name == in.Provider })
	if i < 0 {
		return nil, listGitBranchesOut{}, fmt.Errorf("not found: git provider %s", in.Provider)
	}
	bs, err := t.c.GitProviderBranches(ctx, ps[i].ID, in.Repo)
	return nil, listGitBranchesOut{Branches: bs}, friendly(err)
}

type getTopologyIn struct {
	Project string `json:"project,omitempty" jsonschema:"only this project; default every server and project"`
}

func (t *tools) getTopology(ctx context.Context, _ *mcp.CallToolRequest, in getTopologyIn) (*mcp.CallToolResult, core.Topology, error) {
	if err := core.Require(ctx, store.ScopeRead); err != nil {
		return nil, core.Topology{}, err
	}
	id := ""
	if in.Project != "" {
		p, err := t.findProject(ctx, in.Project)
		if err != nil {
			return nil, core.Topology{}, friendly(err)
		}
		id = p.ID
	}
	topo, err := t.c.Topology(ctx, id)
	return nil, topo, friendly(err)
}

type managerStatusOut struct {
	core.Status
	// Usage is the current use of each running service, by project/service.
	Usage map[string]core.Usage `json:"usage"`
}

func (t *tools) getManagerStatus(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, managerStatusOut, error) {
	if err := core.Require(ctx, store.ScopeRead); err != nil {
		return nil, managerStatusOut{}, err
	}
	out := managerStatusOut{Status: t.c.Status(ctx), Usage: map[string]core.Usage{}}
	usage := t.c.CurrentUsage()
	if len(usage) == 0 {
		return nil, out, nil
	}
	projects, err := t.c.ListProjects(ctx)
	if err != nil {
		return nil, managerStatusOut{}, err
	}
	for _, p := range projects {
		svcs, err := t.c.ListServices(ctx, p.ID)
		if err != nil {
			return nil, managerStatusOut{}, err
		}
		for _, s := range svcs {
			if u, ok := usage[s.ID]; ok {
				out.Usage[p.Name+"/"+s.Name] = u.Usage
			}
		}
	}
	return nil, out, nil
}

type getAuditLogIn struct {
	Limit int `json:"limit,omitempty" jsonschema:"newest first, default 50, max 1000"`
}

type getAuditLogOut struct {
	Entries []store.AuditEntry `json:"entries"`
}

// getAuditLog is admin only, like the REST route: it maps who can do what.
func (t *tools) getAuditLog(ctx context.Context, _ *mcp.CallToolRequest, in getAuditLogIn) (*mcp.CallToolResult, getAuditLogOut, error) {
	if err := core.Require(ctx, store.ScopeAdmin); err != nil {
		return nil, getAuditLogOut{}, err
	}
	n := in.Limit
	if n <= 0 {
		n = 50
	}
	es, err := t.c.ListAudit(ctx, min(n, 1000))
	return nil, getAuditLogOut{Entries: es}, err
}
