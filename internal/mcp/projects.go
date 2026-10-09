package mcp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// Projects, their git link, renames, start/stop and deploy logs.

type ProjectSummary struct {
	Name     string   `json:"name"`
	Server   string   `json:"server"`
	Services []string `json:"services"`
	// Git is the compose file the project follows, when linked.
	Git string `json:"git,omitempty"`
}

type listProjectsOut struct {
	Projects []ProjectSummary `json:"projects"`
}

func (t *tools) listProjects(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listProjectsOut, error) {
	if err := core.Require(ctx, store.ScopeRead); err != nil {
		return nil, listProjectsOut{}, err
	}
	projects, err := t.c.ListProjects(ctx)
	if err != nil {
		return nil, listProjectsOut{}, err
	}
	servers, err := t.c.ListServers(ctx)
	if err != nil {
		return nil, listProjectsOut{}, err
	}
	out := listProjectsOut{Projects: []ProjectSummary{}}
	for _, p := range projects {
		s := ProjectSummary{Name: p.Name, Server: p.ServerID, Services: []string{}}
		if i := slices.IndexFunc(servers, func(sv core.ServerView) bool { return sv.ID == p.ServerID }); i >= 0 {
			s.Server = servers[i].Name
		}
		svcs, err := t.c.ListServices(ctx, p.ID)
		if err != nil {
			return nil, listProjectsOut{}, err
		}
		for _, svc := range svcs {
			s.Services = append(s.Services, svc.Name)
		}
		if g, err := t.c.GetProjectGit(ctx, p.ID); err == nil {
			s.Git = fmt.Sprintf("%s (%s) %s", g.RepoURL, g.Branch, g.Path)
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, listProjectsOut{}, err
		}
		out.Projects = append(out.Projects, s)
	}
	return nil, out, nil
}

type createProjectIn struct {
	Name   string `json:"name" jsonschema:"lowercase letters, digits and dashes (max 40)"`
	Server string `json:"server,omitempty" jsonschema:"the server's name; default: this one"`
}

func (t *tools) createProject(ctx context.Context, _ *mcp.CallToolRequest, in createProjectIn) (*mcp.CallToolResult, ProjectSummary, error) {
	out, err := mutate(ctx, t.c, store.ScopeAdmin, "create_project", in.Name, func() (ProjectSummary, error) {
		serverID := ""
		if in.Server != "" {
			servers, err := t.c.ListServers(ctx)
			if err != nil {
				return ProjectSummary{}, err
			}
			i := slices.IndexFunc(servers, func(sv core.ServerView) bool { return sv.Name == in.Server })
			if i < 0 {
				return ProjectSummary{}, fmt.Errorf("%w: server %s", store.ErrNotFound, in.Server)
			}
			serverID = servers[i].ID
		}
		p, err := t.c.CreateProject(ctx, in.Name, serverID)
		if err != nil {
			return ProjectSummary{}, err
		}
		return ProjectSummary{Name: p.Name, Server: in.Server, Services: []string{}}, nil
	})
	return nil, out, err
}

type renameProjectIn struct {
	Project string `json:"project" jsonschema:"the project's current name"`
	Name    string `json:"name" jsonschema:"its new name"`
}

type renamed struct {
	Name string `json:"name"`
}

func (t *tools) renameProject(ctx context.Context, _ *mcp.CallToolRequest, in renameProjectIn) (*mcp.CallToolResult, renamed, error) {
	out, err := mutate(ctx, t.c, store.ScopeAdmin, "rename_project", in.Project, func() (renamed, error) {
		p, err := t.findProject(ctx, in.Project)
		if err != nil {
			return renamed{}, err
		}
		p, err = t.c.RenameProject(ctx, p.ID, in.Name)
		return renamed{Name: p.Name}, err
	})
	return nil, out, err
}

type renameServiceIn struct {
	Service string `json:"service" jsonschema:"the service as project/service, or its ID"`
	Name    string `json:"name" jsonschema:"its new name, within the same project"`
}

func (t *tools) renameService(ctx context.Context, _ *mcp.CallToolRequest, in renameServiceIn) (*mcp.CallToolResult, renamed, error) {
	out, err := mutate(ctx, t.c, store.ScopeAdmin, "rename_service", in.Service, func() (renamed, error) {
		svc, err := t.c.ResolveService(ctx, in.Service)
		if err != nil {
			return renamed{}, err
		}
		view, err := t.c.RenameService(ctx, svc.ID, in.Name)
		return renamed{Name: view.Name}, err
	})
	return nil, out, err
}

type projectArg struct {
	Project string `json:"project" jsonschema:"the project name"`
}

// GitLink is a project's link without its webhook secret.
type GitLink struct {
	RepoURL     string     `json:"repoUrl"`
	Branch      string     `json:"branch"`
	Path        string     `json:"path"`
	Provider    string     `json:"provider,omitempty"`
	AutoSync    bool       `json:"autoSync"`
	PollSeconds int        `json:"pollSeconds"`
	LastCommit  string     `json:"lastCommit,omitempty"`
	LastSynced  *time.Time `json:"lastSyncedAt,omitempty"`
	LastError   string     `json:"lastError,omitempty"`
	Warnings    []string   `json:"warnings,omitempty"`
	// Drift lists the services running another image than the file's (a
	// tag deployed by CI).
	Drift []string `json:"drift,omitempty"`
}

func (t *tools) gitLink(ctx context.Context, g core.GitStatus) (GitLink, error) {
	out := GitLink{RepoURL: g.RepoURL, Branch: g.Branch, Path: g.Path, AutoSync: g.AutoSync, PollSeconds: g.PollSeconds,
		LastCommit: g.LastCommit, LastSynced: g.LastSyncedAt, LastError: g.LastError, Warnings: g.Warnings, Drift: g.Drift}
	if g.ProviderID != "" {
		ps, err := t.c.ListGitProviders(ctx)
		if err != nil {
			return GitLink{}, err
		}
		if i := slices.IndexFunc(ps, func(p core.GitProviderView) bool { return p.ID == g.ProviderID }); i >= 0 {
			out.Provider = ps[i].Name
		}
	}
	return out, nil
}

func (t *tools) getProjectGit(ctx context.Context, _ *mcp.CallToolRequest, in projectArg) (*mcp.CallToolResult, GitLink, error) {
	if err := core.Require(ctx, store.ScopeRead); err != nil {
		return nil, GitLink{}, err
	}
	p, err := t.findProject(ctx, in.Project)
	if err != nil {
		return nil, GitLink{}, friendly(err)
	}
	g, err := t.c.GetProjectGit(ctx, p.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, GitLink{}, fmt.Errorf("project %s isn't linked to git", in.Project)
	} else if err != nil {
		return nil, GitLink{}, err
	}
	out, err := t.gitLink(ctx, g)
	return nil, out, err
}

type linkProjectGitIn struct {
	Project     string `json:"project" jsonschema:"the project name"`
	RepoURL     string `json:"repo_url" jsonschema:"HTTPS URL of the repository"`
	Branch      string `json:"branch,omitempty" jsonschema:"default main"`
	Path        string `json:"path,omitempty" jsonschema:"the compose file in the repository, default compose.yaml"`
	Provider    string `json:"provider,omitempty" jsonschema:"name of the git provider reading a private repository; empty for a public one"`
	ManualSync  bool   `json:"manual_sync,omitempty" jsonschema:"don't poll the branch; sync only on webhooks or sync_project_git"`
	PollSeconds int    `json:"poll_seconds,omitempty" jsonschema:"how often the branch is checked, 60 to 86400, default 300"`
	DryRun      bool   `json:"dry_run,omitempty" jsonschema:"only return what the first sync would change"`
}

type linkProjectGitOut struct {
	// Plan is what the first sync changes (dry run), or nil once linked.
	Plan *core.ComposePlan `json:"plan,omitempty"`
	Link *GitLink          `json:"link,omitempty"`
}

func (t *tools) linkProjectGit(ctx context.Context, _ *mcp.CallToolRequest, in linkProjectGitIn) (*mcp.CallToolResult, linkProjectGitOut, error) {
	out, err := mutate(ctx, t.c, store.ScopeAdmin, "link_project_git", in.Project, func() (linkProjectGitOut, error) {
		p, err := t.findProject(ctx, in.Project)
		if err != nil {
			return linkProjectGitOut{}, err
		}
		gi := core.GitInput{RepoURL: in.RepoURL, Branch: in.Branch, Path: in.Path, AutoSync: !in.ManualSync, PollSeconds: in.PollSeconds}
		if in.Provider != "" {
			ps, err := t.c.ListGitProviders(ctx)
			if err != nil {
				return linkProjectGitOut{}, err
			}
			i := slices.IndexFunc(ps, func(gp core.GitProviderView) bool { return gp.Name == in.Provider })
			if i < 0 {
				return linkProjectGitOut{}, fmt.Errorf("%w: git provider %s", store.ErrNotFound, in.Provider)
			}
			gi.ProviderID = ps[i].ID
		}
		if in.DryRun {
			plan, err := t.c.PreviewProjectGit(ctx, p.ID, gi)
			return linkProjectGitOut{Plan: &plan}, err
		}
		g, err := t.c.LinkProjectGit(ctx, p.ID, gi)
		if err != nil {
			return linkProjectGitOut{}, err
		}
		link, err := t.gitLink(ctx, g)
		return linkProjectGitOut{Link: &link}, err
	})
	return nil, out, err
}

type syncProjectGitIn struct {
	Project string `json:"project" jsonschema:"the project name"`
	DryRun  bool   `json:"dry_run,omitempty" jsonschema:"only return what the sync would change"`
}

func (t *tools) syncProjectGit(ctx context.Context, _ *mcp.CallToolRequest, in syncProjectGitIn) (*mcp.CallToolResult, core.ComposePlan, error) {
	out, err := mutate(ctx, t.c, store.ScopeAdmin, "sync_project_git", in.Project, func() (core.ComposePlan, error) {
		p, err := t.findProject(ctx, in.Project)
		if err != nil {
			return core.ComposePlan{}, err
		}
		return t.c.SyncProjectGit(ctx, p.ID, in.DryRun)
	})
	return nil, out, err
}

type serviceActionIn struct {
	Service string `json:"service" jsonschema:"the service as project/service, or its ID"`
	Action  string `json:"action" jsonschema:"start, stop or restart"`
}

type serviceActionOut struct {
	Status string `json:"status"`
}

func (t *tools) serviceAction(ctx context.Context, _ *mcp.CallToolRequest, in serviceActionIn) (*mcp.CallToolResult, serviceActionOut, error) {
	out, err := mutate(ctx, t.c, store.ScopeDeploy, "service_action", in.Service, func() (serviceActionOut, error) {
		action := core.Action(in.Action)
		if !slices.Contains([]core.Action{core.ActionStart, core.ActionStop, core.ActionRestart}, action) {
			return serviceActionOut{}, fmt.Errorf("action must be start, stop or restart")
		}
		svc, err := t.c.ResolveService(ctx, in.Service)
		if err != nil {
			return serviceActionOut{}, err
		}
		view, err := t.c.ServiceAction(ctx, svc.ID, action)
		return serviceActionOut{Status: status(view)}, err
	})
	return nil, out, err
}

type getDeploymentLogIn struct {
	Service      string `json:"service,omitempty" jsonschema:"the service as project/service, or its ID: its latest deployment"`
	DeploymentID string `json:"deployment_id,omitempty" jsonschema:"a deployment ID from get_app_status, instead of the latest"`
	Lines        int    `json:"lines,omitempty" jsonschema:"last lines to return, default 200, max 2000"`
}

type getDeploymentLogOut struct {
	Deployment store.Deployment `json:"deployment"`
	Lines      []string         `json:"lines"`
}

func (t *tools) getDeploymentLog(ctx context.Context, _ *mcp.CallToolRequest, in getDeploymentLogIn) (*mcp.CallToolResult, getDeploymentLogOut, error) {
	if err := core.Require(ctx, store.ScopeRead); err != nil {
		return nil, getDeploymentLogOut{}, err
	}
	id := in.DeploymentID
	if id == "" {
		if in.Service == "" {
			return nil, getDeploymentLogOut{}, errors.New("give a service or a deployment_id")
		}
		svc, err := t.c.ResolveService(ctx, in.Service)
		if err != nil {
			return nil, getDeploymentLogOut{}, friendly(err)
		}
		deps, err := t.c.ListDeployments(ctx, svc.ID)
		if err != nil {
			return nil, getDeploymentLogOut{}, err
		}
		if len(deps) == 0 {
			return nil, getDeploymentLogOut{}, fmt.Errorf("%s has no deployments", in.Service)
		}
		id = deps[0].ID
	}
	dep, err := t.c.GetDeployment(ctx, id)
	if err != nil {
		return nil, getDeploymentLogOut{}, friendly(err)
	}
	r, err := t.c.DeploymentLog(ctx, id)
	if err != nil {
		return nil, getDeploymentLogOut{}, err
	}
	defer r.Close()
	n := in.Lines
	if n <= 0 {
		n = 200
	}
	n = min(n, 2000)
	out := getDeploymentLogOut{Deployment: dep, Lines: []string{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		out.Lines = append(out.Lines, sc.Text())
		if len(out.Lines) > n {
			out.Lines = out.Lines[1:]
		}
	}
	return nil, out, sc.Err()
}

type deleteServiceIn struct {
	Service string `json:"service" jsonschema:"the service as project/service, or its ID"`
	Confirm string `json:"confirm" jsonschema:"must equal the service name"`
}

type deleted struct {
	Deleted string `json:"deleted"`
}

// deleteService always wants the confirmation, even for an app without
// data: an agent deleting the wrong service is costly either way.
func (t *tools) deleteService(ctx context.Context, _ *mcp.CallToolRequest, in deleteServiceIn) (*mcp.CallToolResult, deleted, error) {
	out, err := mutate(ctx, t.c, store.ScopeAdmin, "delete_service", in.Service, func() (deleted, error) {
		svc, err := t.c.ResolveService(ctx, in.Service)
		if err != nil {
			return deleted{}, err
		}
		if in.Confirm != svc.Name {
			return deleted{}, errors.New("deleting a service removes its containers and data: confirm must repeat the service name")
		}
		return deleted{Deleted: in.Service}, t.c.DeleteService(ctx, svc.ID, in.Confirm)
	})
	return nil, out, err
}

type deleteProjectIn struct {
	Project string `json:"project" jsonschema:"the project name"`
	Confirm string `json:"confirm" jsonschema:"must equal the project name"`
}

func (t *tools) deleteProject(ctx context.Context, _ *mcp.CallToolRequest, in deleteProjectIn) (*mcp.CallToolResult, deleted, error) {
	out, err := mutate(ctx, t.c, store.ScopeAdmin, "delete_project", in.Project, func() (deleted, error) {
		p, err := t.findProject(ctx, in.Project)
		if err != nil {
			return deleted{}, err
		}
		if in.Confirm != p.Name {
			return deleted{}, errors.New("deleting a project removes all its services and their data: confirm must repeat the project name")
		}
		return deleted{Deleted: p.Name}, t.c.DeleteProject(ctx, p.ID, in.Confirm)
	})
	return nil, out, err
}
