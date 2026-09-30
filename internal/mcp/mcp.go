// Package mcp exposes task-oriented tools to AI agents over the Model
// Context Protocol (Streamable HTTP at /mcp). Tools are thin adapters over
// the core layer; each checks the caller's token scope, masks secrets like
// the REST API and records mutations in the audit log.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// Handler serves MCP. It must run behind authentication that puts a
// core.Actor in the request context.
func Handler(c *core.Core, version string) http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "kipitiny", Version: version}, &mcp.ServerOptions{
		Instructions: "Manage apps and PostgreSQL databases on this kipitiny server. " +
			"Refer to services as project/service. Read tools need a read token, " +
			"deploy/rollback/backup need deploy, restore needs admin and an explicit confirmation.",
	})
	t := &tools{c: c}
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}
	yes := true

	mcp.AddTool(server, &mcp.Tool{Name: "list_services", Annotations: readOnly,
		Description: "List every project with its services, their kind, image or repository, domain and current status."}, t.listServices)
	mcp.AddTool(server, &mcp.Tool{Name: "get_app_status", Annotations: readOnly,
		Description: "Status of one service: containers and health, settings (secrets masked), the last deployments with errors."}, t.getAppStatus)
	mcp.AddTool(server, &mcp.Tool{Name: "get_logs", Annotations: readOnly,
		Description: "Recent log lines of a service's running containers, optionally filtered by a case-insensitive substring."}, t.getLogs)
	mcp.AddTool(server, &mcp.Tool{Name: "deploy",
		Description: "Deploy a service's current settings (zero-downtime for apps; git services are rebuilt). Returns the deployment; poll get_app_status for the outcome."}, t.deploy)
	mcp.AddTool(server, &mcp.Tool{Name: "deploy_from_git",
		Description: "Create (or update) an app built from a Git repository's Dockerfile in a project, then deploy it. The project is created if missing."}, t.deployFromGit)
	mcp.AddTool(server, &mcp.Tool{Name: "rollback",
		Description: "Redeploy the image of an earlier successful deployment (the previous one by default)."}, t.rollback)
	mcp.AddTool(server, &mcp.Tool{Name: "backup_database",
		Description: "Start a pg_dump backup of a PostgreSQL service to a backup target (local disk by default)."}, t.backupDatabase)
	mcp.AddTool(server, &mcp.Tool{Name: "list_backups", Annotations: readOnly,
		Description: "Backups of a PostgreSQL service with status, size, target and restore-test result."}, t.listBackups)
	mcp.AddTool(server, &mcp.Tool{Name: "restore_database", Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes},
		Description: "Replace a database's data with a backup. Destructive: confirm must repeat the database's service name. Linked apps are stopped meanwhile."}, t.restoreDatabase)

	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
	})
}

type tools struct{ c *core.Core }

// mutate checks the scope, runs f and records the outcome in the audit log.
func mutate[T any](ctx context.Context, c *core.Core, scope store.Scope, action, target string, f func() (T, error)) (T, error) {
	var zero T
	if err := core.Require(ctx, scope); err != nil {
		c.Audit(ctx, "mcp "+action, target, http.StatusForbidden, err) // denied attempts matter too
		return zero, err
	}
	out, err := f()
	status := http.StatusOK
	if err != nil {
		status = http.StatusBadRequest
	}
	c.Audit(ctx, "mcp "+action, target, status, err)
	return out, friendly(err)
}

// friendly turns internal sentinel prefixes into plain messages for agents.
func friendly(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("not found: %s", strings.TrimPrefix(err.Error(), "not found: "))
	case errors.Is(err, core.ErrBusy):
		return errors.New("another operation (deploy, backup, restore) is running on this service; retry shortly")
	}
	return err
}

type serviceArg struct {
	Service string `json:"service" jsonschema:"the service as project/service, or its ID"`
}

type ServiceSummary struct {
	Project string `json:"project"`
	Service string `json:"service"`
	Kind    string `json:"kind"`
	Source  string `json:"source"`
	Domain  string `json:"domain,omitempty"`
	Status  string `json:"status"`
}

type listServicesOut struct {
	Services []ServiceSummary `json:"services"`
}

func (t *tools) listServices(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listServicesOut, error) {
	if err := core.Require(ctx, store.ScopeRead); err != nil {
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
			src := s.Image
			if s.Source == store.SourceGit {
				src = s.GitURL + "@" + s.GitBranch
			}
			out.Services = append(out.Services, ServiceSummary{
				Project: p.Name, Service: s.Name, Kind: string(s.Kind), Source: src,
				Domain: s.Domain, Status: status(s),
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
	Service     core.ServiceView   `json:"service"`
	Status      string             `json:"status"`
	Deployments []store.Deployment `json:"recentDeployments"`
}

func (t *tools) getAppStatus(ctx context.Context, _ *mcp.CallToolRequest, in serviceArg) (*mcp.CallToolResult, appStatusOut, error) {
	if err := core.Require(ctx, store.ScopeRead); err != nil {
		return nil, appStatusOut{}, err
	}
	svc, err := t.c.ResolveService(ctx, in.Service)
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
	return nil, appStatusOut{Service: view, Status: status(view), Deployments: deps[:min(len(deps), 5)]}, nil
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
	if err := core.Require(ctx, store.ScopeRead); err != nil {
		return nil, getLogsOut{}, err
	}
	svc, err := t.c.ResolveService(ctx, in.Service)
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

func (t *tools) deploy(ctx context.Context, _ *mcp.CallToolRequest, in serviceArg) (*mcp.CallToolResult, store.Deployment, error) {
	dep, err := mutate(ctx, t.c, store.ScopeDeploy, "deploy", in.Service, func() (store.Deployment, error) {
		svc, err := t.c.ResolveService(ctx, in.Service)
		if err != nil {
			return store.Deployment{}, err
		}
		return t.c.Deploy(ctx, svc.ID)
	})
	return nil, dep, err
}

type deployFromGitIn struct {
	Project    string            `json:"project" jsonschema:"project name (created if missing)"`
	Name       string            `json:"name" jsonschema:"service name (lowercase, digits, dashes)"`
	Repository string            `json:"repository" jsonschema:"HTTPS URL of the Git repository"`
	Branch     string            `json:"branch,omitempty" jsonschema:"branch to build, default main"`
	Dockerfile string            `json:"dockerfile,omitempty" jsonschema:"Dockerfile path, default Dockerfile"`
	Domain     string            `json:"domain,omitempty" jsonschema:"public hostname; omit for a private service"`
	Port       int               `json:"port,omitempty" jsonschema:"container port the app listens on (required with a domain)"`
	Env        map[string]string `json:"env,omitempty" jsonschema:"environment variables to set"`
}

func (t *tools) deployFromGit(ctx context.Context, _ *mcp.CallToolRequest, in deployFromGitIn) (*mcp.CallToolResult, store.Deployment, error) {
	dep, err := mutate(ctx, t.c, store.ScopeDeploy, "deploy_from_git", in.Project+"/"+in.Name, func() (store.Deployment, error) {
		project, err := t.findOrCreateProject(ctx, in.Project)
		if err != nil {
			return store.Deployment{}, err
		}
		svc, err := t.c.ResolveService(ctx, project.Name+"/"+in.Name)
		switch {
		case errors.Is(err, store.ErrNotFound):
			view, err := t.c.CreateService(ctx, project.ID, core.ServiceInput{
				Name: in.Name, Source: store.SourceGit, GitURL: in.Repository, GitBranch: in.Branch,
				Dockerfile: in.Dockerfile, Domain: in.Domain, Port: in.Port, Env: in.Env,
			})
			if err != nil {
				return store.Deployment{}, err
			}
			svc = view.Service
		case err != nil:
			return store.Deployment{}, err
		default:
			if svc.Source != store.SourceGit {
				return store.Deployment{}, fmt.Errorf("%s/%s exists and is not built from Git", project.Name, in.Name)
			}
			patch := core.ServicePatch{GitURL: &in.Repository, Domain: &in.Domain, Port: &in.Port}
			if in.Branch != "" {
				patch.GitBranch = &in.Branch
			}
			if in.Dockerfile != "" {
				patch.Dockerfile = &in.Dockerfile
			}
			if in.Env != nil {
				// Keep existing variables; masked values mean "unchanged".
				env := map[string]string{}
				for k := range svc.Env {
					env[k] = core.SecretMask
				}
				for k, v := range in.Env {
					env[k] = v
				}
				patch.Env = env
			}
			if _, err := t.c.UpdateService(ctx, svc.ID, patch); err != nil {
				return store.Deployment{}, err
			}
		}
		return t.c.Deploy(ctx, svc.ID)
	})
	return nil, dep, err
}

func (t *tools) findOrCreateProject(ctx context.Context, name string) (store.Project, error) {
	projects, err := t.c.ListProjects(ctx)
	if err != nil {
		return store.Project{}, err
	}
	for _, p := range projects {
		if p.Name == name {
			return p, nil
		}
	}
	return t.c.CreateProject(ctx, name)
}

type rollbackIn struct {
	Service      string `json:"service" jsonschema:"the service as project/service, or its ID"`
	DeploymentID string `json:"deployment_id,omitempty" jsonschema:"deployment to return to; default: the previous successful one"`
}

func (t *tools) rollback(ctx context.Context, _ *mcp.CallToolRequest, in rollbackIn) (*mcp.CallToolResult, store.Deployment, error) {
	dep, err := mutate(ctx, t.c, store.ScopeDeploy, "rollback", in.Service, func() (store.Deployment, error) {
		svc, err := t.c.ResolveService(ctx, in.Service)
		if err != nil {
			return store.Deployment{}, err
		}
		return t.c.Rollback(ctx, svc.ID, in.DeploymentID)
	})
	return nil, dep, err
}

type backupIn struct {
	Database string `json:"database" jsonschema:"the PostgreSQL service as project/service, or its ID"`
	Target   string `json:"target,omitempty" jsonschema:"backup target ID; default local disk"`
}

func (t *tools) backupDatabase(ctx context.Context, _ *mcp.CallToolRequest, in backupIn) (*mcp.CallToolResult, store.Backup, error) {
	b, err := mutate(ctx, t.c, store.ScopeDeploy, "backup_database", in.Database, func() (store.Backup, error) {
		svc, err := t.c.ResolveService(ctx, in.Database)
		if err != nil {
			return store.Backup{}, err
		}
		return t.c.BackupDatabase(ctx, svc.ID, in.Target)
	})
	return nil, b, err
}

type listBackupsIn struct {
	Database string `json:"database" jsonschema:"the PostgreSQL service as project/service, or its ID"`
}

type listBackupsOut struct {
	Backups []store.Backup `json:"backups"`
}

func (t *tools) listBackups(ctx context.Context, _ *mcp.CallToolRequest, in listBackupsIn) (*mcp.CallToolResult, listBackupsOut, error) {
	if err := core.Require(ctx, store.ScopeRead); err != nil {
		return nil, listBackupsOut{}, err
	}
	svc, err := t.c.ResolveService(ctx, in.Database)
	if err != nil {
		return nil, listBackupsOut{}, friendly(err)
	}
	bs, err := t.c.ListBackups(ctx, store.BackupFilter{ServiceID: svc.ID, Limit: 50})
	return nil, listBackupsOut{Backups: bs}, err
}

type restoreIn struct {
	Database string `json:"database" jsonschema:"the PostgreSQL service to overwrite, as project/service or ID"`
	BackupID string `json:"backup_id" jsonschema:"backup to restore (see list_backups)"`
	Confirm  string `json:"confirm" jsonschema:"must equal the database's service name to confirm the data will be replaced"`
}

func (t *tools) restoreDatabase(ctx context.Context, _ *mcp.CallToolRequest, in restoreIn) (*mcp.CallToolResult, store.Restore, error) {
	r, err := mutate(ctx, t.c, store.ScopeAdmin, "restore_database", in.Database, func() (store.Restore, error) {
		svc, err := t.c.ResolveService(ctx, in.Database)
		if err != nil {
			return store.Restore{}, err
		}
		return t.c.RestoreBackup(ctx, in.BackupID, svc.ID, in.Confirm)
	})
	return nil, r, err
}
