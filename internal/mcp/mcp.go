// Package mcp exposes task-oriented tools to AI agents over the Model
// Context Protocol (Streamable HTTP at /mcp). Tools are thin adapters over
// the core layer; each checks the caller's token scope, masks secrets like
// the REST API and records mutations in the audit log.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
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
		Description: "List every project with its services, their kind, image, domain and current status."}, t.listServices)
	mcp.AddTool(server, &mcp.Tool{Name: "get_app_status", Annotations: readOnly,
		Description: "Status of one service: containers and health, settings (secrets masked), the last deployments with errors."}, t.getAppStatus)
	mcp.AddTool(server, &mcp.Tool{Name: "get_logs", Annotations: readOnly,
		Description: "Recent log lines of a service's running containers, optionally filtered by a case-insensitive substring."}, t.getLogs)
	mcp.AddTool(server, &mcp.Tool{Name: "deploy",
		Description: "Deploy a service's current settings (zero-downtime for apps), or another tag of an app's image. Returns the deployment; poll get_app_status for the outcome."}, t.deploy)
	mcp.AddTool(server, &mcp.Tool{Name: "deploy_image",
		Description: "Create (or update) an app running a Docker image in a project, then deploy it. The project is created if missing."}, t.deployImage)
	mcp.AddTool(server, &mcp.Tool{Name: "set_project_env",
		Description: "Set or remove a project's shared variables (readable) and secrets (write-only). Services use either as {{ project.NAME }} in an env value; they pick up changes on their next deploy."}, t.setProjectEnv)
	mcp.AddTool(server, &mcp.Tool{Name: "rollback",
		Description: "Redeploy the image of an earlier successful deployment (the previous one by default)."}, t.rollback)
	mcp.AddTool(server, &mcp.Tool{Name: "backup_database",
		Description: "Start a pg_dump backup of a PostgreSQL service to a backup target (local disk by default)."}, t.backupDatabase)
	mcp.AddTool(server, &mcp.Tool{Name: "list_backups", Annotations: readOnly,
		Description: "Backups of a PostgreSQL service with status, size, target and restore-test result."}, t.listBackups)
	mcp.AddTool(server, &mcp.Tool{Name: "restore_database", Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes},
		Description: "Replace a database's data with a backup. Destructive: confirm must repeat the database's service name. Apps referencing it are stopped meanwhile."}, t.restoreDatabase)

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
	Image   string `json:"image"`
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
			out.Services = append(out.Services, ServiceSummary{
				Project: p.Name, Service: s.Name, Kind: string(s.Kind), Image: s.Image,
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

type deployIn struct {
	Service string `json:"service" jsonschema:"the service as project/service, or its ID"`
	Tag     string `json:"tag,omitempty" jsonschema:"image services only: deploy this tag of the service's image, which then keeps it"`
}

func (t *tools) deploy(ctx context.Context, _ *mcp.CallToolRequest, in deployIn) (*mcp.CallToolResult, store.Deployment, error) {
	dep, err := mutate(ctx, t.c, store.ScopeDeploy, "deploy", in.Service, func() (store.Deployment, error) {
		svc, err := t.c.ResolveService(ctx, in.Service)
		if err != nil {
			return store.Deployment{}, err
		}
		return t.c.Deploy(ctx, svc.ID, core.DeployOptions{Tag: in.Tag})
	})
	return nil, dep, err
}

type deployImageIn struct {
	Project string            `json:"project" jsonschema:"project name (created if missing)"`
	Name    string            `json:"name" jsonschema:"service name (lowercase, digits, dashes)"`
	Image   string            `json:"image" jsonschema:"Docker image, e.g. ghcr.io/org/app:v1"`
	Domain  string            `json:"domain,omitempty" jsonschema:"public hostname; omit for a private service"`
	Port    int               `json:"port,omitempty" jsonschema:"container port the app listens on (required with a domain)"`
	Env     map[string]string `json:"env,omitempty" jsonschema:"environment variables to set; a value may reference a project variable as {{ project.NAME }}"`
}

func (t *tools) deployImage(ctx context.Context, _ *mcp.CallToolRequest, in deployImageIn) (*mcp.CallToolResult, store.Deployment, error) {
	dep, err := mutate(ctx, t.c, store.ScopeDeploy, "deploy_image", in.Project+"/"+in.Name, func() (store.Deployment, error) {
		project, err := t.findOrCreateProject(ctx, in.Project)
		if err != nil {
			return store.Deployment{}, err
		}
		svc, err := t.c.ResolveService(ctx, project.Name+"/"+in.Name)
		switch {
		case errors.Is(err, store.ErrNotFound):
			view, err := t.c.CreateService(ctx, project.ID, core.ServiceInput{
				Name: in.Name, Image: in.Image, Domain: in.Domain, Port: in.Port, Env: in.Env,
			})
			if err != nil {
				return store.Deployment{}, err
			}
			svc = view.Service
		case err != nil:
			return store.Deployment{}, err
		default:
			if svc.Kind != store.ServiceKindApp {
				return store.Deployment{}, fmt.Errorf("%s/%s exists and is not an app", project.Name, in.Name)
			}
			patch := core.ServicePatch{Image: &in.Image, Domain: &in.Domain, Port: &in.Port}
			if in.Env != nil {
				// Keep existing entries; new ones become secrets.
				env := maps.Clone(svc.Env)
				maps.Copy(env, in.Env)
				patch.Env = env
			}
			if _, err := t.c.UpdateService(ctx, svc.ID, patch); err != nil {
				return store.Deployment{}, err
			}
		}
		return t.c.Deploy(ctx, svc.ID, core.DeployOptions{})
	})
	return nil, dep, err
}

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
	out, err := mutate(ctx, t.c, store.ScopeAdmin, "set_project_env", in.Project, func() (setProjectEnvOut, error) {
		project, err := t.findProject(ctx, in.Project)
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

func (t *tools) findProject(ctx context.Context, name string) (store.Project, error) {
	projects, err := t.c.ListProjects(ctx)
	if err != nil {
		return store.Project{}, err
	}
	for _, p := range projects {
		if p.Name == name {
			return p, nil
		}
	}
	return store.Project{}, fmt.Errorf("%w: project %s", store.ErrNotFound, name)
}

func (t *tools) findOrCreateProject(ctx context.Context, name string) (store.Project, error) {
	p, err := t.findProject(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return t.c.CreateProject(ctx, name, "")
	}
	return p, err
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
