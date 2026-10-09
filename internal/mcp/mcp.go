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
	"regexp"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/store"
)

var release = regexp.MustCompile(`^(\d+\.\d+)\.\d+$`)

// docs returns where the docs of this manager version are: the site's index
// for LLMs, the base of its pages (llms.txt per page) and their sources. A
// release reads its minor's copy (/docs/0.10/...), a dev build the latest.
func docs(version string) (index, base, sources string) {
	const site = "https://kipitiny.mathieuhoyer.fr"
	if m := release.FindStringSubmatch(version); m != nil {
		base = site + "/docs/" + m[1]
		return base + "/llms.txt", base, "https://github.com/MatHoyer/kipitiny/tree/" + version + "/site/docs"
	}
	return site + "/llms.txt", site + "/docs", "https://github.com/MatHoyer/kipitiny/tree/main/site/docs"
}

// Handler serves MCP. It must run behind authentication that puts a
// core.Actor in the request context.
func Handler(c *core.Core, version string) http.Handler {
	docsIndex, docsBase, docsSources := docs(version)
	server := mcp.NewServer(&mcp.Implementation{Name: "kipitiny", Version: version}, &mcp.ServerOptions{
		Instructions: "Manage apps and PostgreSQL/Redis databases on this kipitiny server. " +
			"Refer to services as project/service. Read tools need a read token; " +
			"deploy, rollback, service_action, backup_database, backup_project and verify_backup need deploy (with deploy, deploy_image only changes the tag of an existing app); " +
			"the other tools need admin (each says so), and deleting a service or project or restoring a backup takes its name as an explicit confirmation. " +
			"Documentation of this version, as markdown: " + docsIndex + " lists every page; read the relevant one before guessing how a feature works. " +
			"If the site is unreachable, the same pages are in " + docsSources + ".",
	})
	t := &tools{c: c}
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}
	yes := true

	mcp.AddTool(server, &mcp.Tool{Name: "list_services", Annotations: readOnly,
		Description: "List every project with its services, their kind, image, domain and current status."}, t.listServices)
	mcp.AddTool(server, &mcp.Tool{Name: "list_projects", Annotations: readOnly,
		Description: "List every project, including empty ones, with its server, service names and the git compose file it follows, if any."}, t.listProjects)
	mcp.AddTool(server, &mcp.Tool{Name: "get_manager_status", Annotations: readOnly,
		Description: "The manager's version and available update, its Docker engine, and the current CPU, memory and network use of every running service."}, t.getManagerStatus)
	mcp.AddTool(server, &mcp.Tool{Name: "get_topology", Annotations: readOnly,
		Description: "The network map of each server: entrypoints and the reverse proxy, Docker networks with subnets, and each project's containers with their IPs and aliases."}, t.getTopology)
	mcp.AddTool(server, &mcp.Tool{Name: "get_audit_log", Annotations: readOnly,
		Description: "Recent mutations by users, tokens and agents, including refused ones: actor, action, target, status and error. Needs admin."}, t.getAuditLog)
	mcp.AddTool(server, &mcp.Tool{Name: "create_project",
		Description: "Create an empty project on a server (this one by default). Add services with deploy_image, apply_project_compose or link_project_git. Needs admin."}, t.createProject)
	mcp.AddTool(server, &mcp.Tool{Name: "rename_project",
		Description: "Rename a project. Its containers are renamed in place; nothing restarts. Needs admin."}, t.renameProject)
	mcp.AddTool(server, &mcp.Tool{Name: "rename_service",
		Description: "Rename a service. Nothing restarts; until its next deploy it also answers to its old name on the project network. Renaming a database rewrites the {{ db.NAME.* }} references to it and redeploys the apps using it. Not for projects linked to git (edit the file). Needs admin."}, t.renameService)
	mcp.AddTool(server, &mcp.Tool{Name: "delete_service", Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes},
		Description: "Delete a service: its containers, volumes (data) and deploy logs; backups are kept. confirm must repeat the service name. " +
			"A database still referenced by an app, or a service of a git-linked project, is refused. Needs admin."}, t.deleteService)
	mcp.AddTool(server, &mcp.Tool{Name: "delete_project", Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes},
		Description: "Delete a project with every service, volume and its network; backups are kept. confirm must repeat the project name. Needs admin."}, t.deleteProject)
	mcp.AddTool(server, &mcp.Tool{Name: "get_project_git", Annotations: readOnly,
		Description: "A project's git link: repository, branch, compose file path, auto sync, the last sync's commit, time, error and warnings, and the services running another image than the file's."}, t.getProjectGit)
	mcp.AddTool(server, &mcp.Tool{Name: "link_project_git", Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes},
		Description: "Link a project to a compose file in a git repository (see " + docsBase + "/compose/llms.txt). The file then owns the services: every commit is applied and apps it doesn't list are deleted with their volumes. " +
			"Run with dry_run first to see what the first sync changes. Needs admin."}, t.linkProjectGit)
	mcp.AddTool(server, &mcp.Tool{Name: "sync_project_git", Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes},
		Description: "Apply a git-linked project's compose file now, as a push would. dry_run returns the plan only. Needs admin."}, t.syncProjectGit)
	mcp.AddTool(server, &mcp.Tool{Name: "list_git_repos", Annotations: readOnly,
		Description: "Repositories the connected git providers can read (full name, clone URL, default branch), to pick one for link_project_git."}, t.listGitRepos)
	mcp.AddTool(server, &mcp.Tool{Name: "list_git_branches", Annotations: readOnly,
		Description: "Branches of a repository a git provider can read."}, t.listGitBranches)
	mcp.AddTool(server, &mcp.Tool{Name: "service_action",
		Description: "Start, stop or restart a service. A stopped service stays stopped until started."}, t.serviceAction)
	mcp.AddTool(server, &mcp.Tool{Name: "get_deployment_log", Annotations: readOnly,
		Description: "The log of a deployment (pull, readiness, errors): a service's latest one, or one by ID from get_app_status."}, t.getDeploymentLog)
	mcp.AddTool(server, &mcp.Tool{Name: "get_app_status", Annotations: readOnly,
		Description: "Status of one service: containers and health, settings (secrets masked), the last deployments with errors, current CPU/memory/network use, uptime check results."}, t.getAppStatus)
	mcp.AddTool(server, &mcp.Tool{Name: "get_logs", Annotations: readOnly,
		Description: "Recent log lines of a service's running containers, optionally filtered by a case-insensitive substring."}, t.getLogs)
	mcp.AddTool(server, &mcp.Tool{Name: "deploy",
		Description: "Deploy a service's current settings (zero-downtime for apps without published ports), or another tag of an app's image. Returns the deployment; poll get_app_status for the outcome."}, t.deploy)
	mcp.AddTool(server, &mcp.Tool{Name: "deploy_image",
		Description: "Create (or update) an app running a Docker image in a project, then deploy it. The project is created if missing. With a deploy token, only an existing app's image tag or digest can change; anything else needs admin."}, t.deployImage)
	mcp.AddTool(server, &mcp.Tool{Name: "set_project_env",
		Description: "Set or remove a project's shared variables (readable) and secrets (write-only). Services use either as {{ project.NAME }} in an env value; they pick up changes on their next deploy."}, t.setProjectEnv)
	mcp.AddTool(server, &mcp.Tool{Name: "get_project_compose", Annotations: readOnly,
		Description: "A project (or one of its services) as an equivalent docker-compose file, kipitiny settings in x-kipitiny blocks. Secret values are ${NAME} variables without values."}, t.getProjectCompose)
	mcp.AddTool(server, &mcp.Tool{Name: "apply_project_compose", Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes},
		Description: "Make a project match a docker-compose file (format of get_project_compose, fully documented at " + docsBase + "/compose/llms.txt): creates and updates services, then deploys the changed ones. " +
			"dry_run returns the plan only. prune also deletes the apps the file doesn't list (with their volumes): confirm must repeat the project name. Needs admin."}, t.applyProjectCompose)
	mcp.AddTool(server, &mcp.Tool{Name: "rollback",
		Description: "Redeploy the image of an earlier successful deployment (the previous one by default)."}, t.rollback)
	mcp.AddTool(server, &mcp.Tool{Name: "backup_database",
		Description: "Start a backup of a service to a storage (local disk by default; see list_storage): a pg_dump of a PostgreSQL service, an archive of the volumes of a Redis service or an app with volumes."}, t.backupDatabase)
	mcp.AddTool(server, &mcp.Tool{Name: "list_backups", Annotations: readOnly,
		Description: "Backups of a service with status, size, storage (targetId) and restore-test result."}, t.listBackups)
	mcp.AddTool(server, &mcp.Tool{Name: "list_storage", Annotations: readOnly,
		Description: "Where kipitiny can store files (local disk, S3, drives), with their IDs and whether files are encrypted."}, t.listStorage)
	mcp.AddTool(server, &mcp.Tool{Name: "restore_database", Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes},
		Description: "Replace a service's data with a backup. Destructive: confirm must repeat the service name. For a PostgreSQL dump, apps referencing the database are stopped meanwhile; for a volume archive, the service itself is."}, t.restoreDatabase)

	mcp.AddTool(server, &mcp.Tool{Name: "backup_project",
		Description: "Back up every database and app with volumes of a project to one storage. Services whose backup can't start are listed in error; the others still run."}, t.backupProject)
	mcp.AddTool(server, &mcp.Tool{Name: "verify_backup",
		Description: "Restore-test a successful backup in a throwaway, network-less container; list_backups then shows the result."}, t.verifyBackup)
	mcp.AddTool(server, &mcp.Tool{Name: "delete_backup", Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes},
		Description: "Delete a backup and its file from storage. Needs admin."}, t.deleteBackup)
	mcp.AddTool(server, &mcp.Tool{Name: "list_restores", Annotations: readOnly,
		Description: "Restores of a service: which backup, when and the outcome."}, t.listRestores)
	mcp.AddTool(server, &mcp.Tool{Name: "list_deployments", Annotations: readOnly,
		Description: "A service's deployments, newest first: image, status, error, commit and who triggered it. IDs work with get_deployment_log and rollback."}, t.listDeployments)
	mcp.AddTool(server, &mcp.Tool{Name: "list_backup_schedules", Annotations: readOnly,
		Description: "A service's backup schedules: cron (UTC), storage, retention, restore tests and next run."}, t.listBackupSchedules)
	mcp.AddTool(server, &mcp.Tool{Name: "set_backup_schedule",
		Description: "Add a backup schedule to a service, or change one (schedule_id; omitted fields are kept). Each schedule prunes its own backups by its retention. Needs admin."}, t.setBackupSchedule)
	mcp.AddTool(server, &mcp.Tool{Name: "delete_backup_schedule",
		Description: "Delete one of a service's backup schedules; the backups it made are kept. Needs admin."}, t.deleteBackupSchedule)
	mcp.AddTool(server, &mcp.Tool{Name: "set_uptime_check",
		Description: "Create or replace the uptime check of an app with a public domain: an HTTPS request to a path at an interval, notifying when it goes down. It runs right away; get_app_status shows its results. Needs admin."}, t.setUptimeCheck)
	mcp.AddTool(server, &mcp.Tool{Name: "delete_uptime_check",
		Description: "Remove a service's uptime check and its history. Needs admin."}, t.deleteUptimeCheck)

	mcp.AddTool(server, &mcp.Tool{Name: "list_databases", Annotations: readOnly,
		Description: "The databases of a PostgreSQL service's instance, with their size; main is the service's own."}, t.listDatabases)
	mcp.AddTool(server, &mcp.Tool{Name: "create_database",
		Description: "Create a database in a PostgreSQL service's instance, owned by the service's user. Needs admin."}, t.createDatabase)
	mcp.AddTool(server, &mcp.Tool{Name: "list_tables", Annotations: readOnly,
		Description: "Tables and views of a PostgreSQL database with their columns, estimated rows and size."}, t.listTables)
	mcp.AddTool(server, &mcp.Tool{Name: "read_table", Annotations: readOnly,
		Description: "Rows of a PostgreSQL table or view, paged, sorted and filtered. Cells are text (null is NULL); long ones are cut and listed in truncated."}, t.readTable)
	mcp.AddTool(server, &mcp.Tool{Name: "scan_redis_keys", Annotations: readOnly,
		Description: "One SCAN step over a Redis service's keys, with their type, TTL and size. Call again with the returned cursor until it is 0."}, t.scanRedisKeys)
	mcp.AddTool(server, &mcp.Tool{Name: "get_redis_key", Annotations: readOnly,
		Description: "One page of a Redis key's value: [value] for a string, [field, value] pairs for a hash, [member, score] for a sorted set."}, t.getRedisKey)
	mcp.AddTool(server, &mcp.Tool{Name: "query_database", Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes},
		Description: "Run SQL on a PostgreSQL service or a command on a Redis one; read-only unless write is set. Prefer read_table and get_redis_key to look at data. Needs admin."}, t.queryDatabase)
	mcp.AddTool(server, &mcp.Tool{Name: "get_connection",
		Description: "A database service's host, port, user, password and URL, as apps of its project reach it. Needs admin."}, t.getConnection)

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
	// Usage is the current CPU, memory and network use; nil when not running.
	Usage *core.Usage `json:"usage,omitempty"`
	// Uptime is the uptime check and its results, when the service has one.
	Uptime *core.UptimeView `json:"uptime,omitempty"`
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
	stats, err := t.c.ServiceStats(ctx, svc.ID)
	if err != nil {
		return nil, appStatusOut{}, err
	}
	out := appStatusOut{Service: view, Status: status(view), Deployments: deps[:min(len(deps), 5)], Usage: stats.Current}
	up, err := t.c.Uptime(ctx, svc.ID)
	if err != nil {
		return nil, appStatusOut{}, err
	}
	if up.Check != nil {
		out.Uptime = &up
	}
	return nil, out, nil
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
	Port    int               `json:"port,omitempty" jsonschema:"container port Traefik routes HTTPS to (required with a domain, unless the app publishes ports)"`
	Env     map[string]string `json:"env,omitempty" jsonschema:"environment variables to set; a value may reference a project variable as {{ project.NAME }}"`
	// Pointers: omitted keeps an existing service's value.
	PublishedPorts   *[]publishedPortIn `json:"publishedPorts,omitempty" jsonschema:"host ports bound straight to the container, for non-HTTP traffic (e.g. a game server); the app then runs one replica and deploys stop-then-start"`
	StopGraceSeconds *int               `json:"stopGraceSeconds,omitempty" jsonschema:"seconds the app gets to exit after SIGTERM before it is killed (default 10, max 600)"`
}

type publishedPortIn struct {
	HostPort      int    `json:"hostPort" jsonschema:"port on the server"`
	ContainerPort int    `json:"containerPort,omitempty" jsonschema:"port in the container; defaults to hostPort"`
	Protocol      string `json:"protocol,omitempty" jsonschema:"tcp (default) or udp"`
}

func (in deployImageIn) ports() []store.PublishedPort {
	if in.PublishedPorts == nil {
		return nil
	}
	out := make([]store.PublishedPort, 0, len(*in.PublishedPorts))
	for _, p := range *in.PublishedPorts {
		out = append(out, store.PublishedPort{HostPort: p.HostPort, ContainerPort: p.ContainerPort, Protocol: p.Protocol})
	}
	return out
}

// deployImage needs admin to create apps or change anything but the tag: it
// could otherwise run any image with env that references password-manager
// and database secrets, publish host ports and claim domains. A deploy token
// gets what REST deploy allows, another tag of an existing app's image.
func (t *tools) deployImage(ctx context.Context, _ *mcp.CallToolRequest, in deployImageIn) (*mcp.CallToolResult, store.Deployment, error) {
	dep, err := mutate(ctx, t.c, store.ScopeDeploy, "deploy_image", in.Project+"/"+in.Name, func() (store.Deployment, error) {
		if core.Require(ctx, store.ScopeAdmin) != nil {
			return t.retag(ctx, in)
		}
		project, err := t.findOrCreateProject(ctx, in.Project)
		if err != nil {
			return store.Deployment{}, err
		}
		svc, err := t.c.ResolveService(ctx, project.Name+"/"+in.Name)
		switch {
		case errors.Is(err, store.ErrNotFound):
			input := core.ServiceInput{
				Name: in.Name, Image: in.Image, Domain: in.Domain, Port: in.Port, Env: in.Env, PublishedPorts: in.ports(),
			}
			if in.StopGraceSeconds != nil {
				input.StopGraceSeconds = *in.StopGraceSeconds
			}
			view, err := t.c.CreateService(ctx, project.ID, input)
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
			patch := core.ServicePatch{Image: &in.Image, Domain: &in.Domain, Port: &in.Port,
				PublishedPorts: in.ports(), StopGraceSeconds: in.StopGraceSeconds}
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

// retag is deploy_image for a deploy token: the app must exist and only its
// image tag or digest may change.
func (t *tools) retag(ctx context.Context, in deployImageIn) (store.Deployment, error) {
	svc, err := t.c.ResolveService(ctx, in.Project+"/"+in.Name)
	if errors.Is(err, store.ErrNotFound) {
		return store.Deployment{}, fmt.Errorf("%w: needs the admin scope to create an app", core.ErrForbidden)
	}
	if err != nil {
		return store.Deployment{}, err
	}
	if svc.Kind != store.ServiceKindApp {
		return store.Deployment{}, fmt.Errorf("%s/%s exists and is not an app", in.Project, in.Name)
	}
	if in.Env != nil || in.PublishedPorts != nil || in.StopGraceSeconds != nil ||
		(in.Domain != "" && in.Domain != svc.Domain) || (in.Port != 0 && in.Port != svc.Port) {
		return store.Deployment{}, fmt.Errorf("%w: needs the admin scope to change anything but the image tag", core.ErrForbidden)
	}
	opts, err := core.RetagOptions(svc.Image, in.Image)
	if err != nil {
		return store.Deployment{}, err
	}
	return t.c.Deploy(ctx, svc.ID, opts)
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

type getProjectComposeIn struct {
	Project string `json:"project" jsonschema:"the project name"`
	Service string `json:"service,omitempty" jsonschema:"only this service of the project"`
}

type getProjectComposeOut struct {
	Compose string `json:"compose"`
}

func (t *tools) getProjectCompose(ctx context.Context, _ *mcp.CallToolRequest, in getProjectComposeIn) (*mcp.CallToolResult, getProjectComposeOut, error) {
	if err := core.Require(ctx, store.ScopeRead); err != nil {
		return nil, getProjectComposeOut{}, err
	}
	project, err := t.findProject(ctx, in.Project)
	if err != nil {
		return nil, getProjectComposeOut{}, friendly(err)
	}
	out, err := t.c.ExportCompose(ctx, project.ID, core.ExportOptions{Service: in.Service})
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

func (t *tools) applyProjectCompose(ctx context.Context, _ *mcp.CallToolRequest, in applyProjectComposeIn) (*mcp.CallToolResult, core.ComposePlan, error) {
	out, err := mutate(ctx, t.c, store.ScopeAdmin, "apply_project_compose", in.Project, func() (core.ComposePlan, error) {
		project, err := t.findProject(ctx, in.Project)
		if err != nil {
			return core.ComposePlan{}, err
		}
		if in.Prune && !in.DryRun && in.Confirm != project.Name {
			return core.ComposePlan{}, errors.New("prune deletes apps and their data: confirm must repeat the project name")
		}
		return t.c.ApplyCompose(ctx, project.ID, []byte(in.Compose), core.ApplyOptions{DryRun: in.DryRun, Prune: in.Prune, Deploy: true})
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
	Database string `json:"database" jsonschema:"the service to back up (PostgreSQL, Redis or an app with volumes) as project/service, or its ID"`
	Storage  string `json:"storage,omitempty" jsonschema:"storage ID (see list_storage); default local disk"`
	// The service is "database" for compatibility; this picks inside it.
	DatabaseName string `json:"database_name,omitempty" jsonschema:"for PostgreSQL, which of the instance's databases to dump; default the service's own"`
}

func (t *tools) backupDatabase(ctx context.Context, _ *mcp.CallToolRequest, in backupIn) (*mcp.CallToolResult, store.Backup, error) {
	b, err := mutate(ctx, t.c, store.ScopeDeploy, "backup_database", in.Database, func() (store.Backup, error) {
		svc, err := t.c.ResolveService(ctx, in.Database)
		if err != nil {
			return store.Backup{}, err
		}
		return t.c.BackupService(ctx, svc.ID, in.Storage, in.DatabaseName)
	})
	return nil, b, err
}

// StorageSummary is a storage as agents see it: no endpoints or credentials.
type StorageSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Encrypted bool   `json:"encrypted"`
}

type listStorageOut struct {
	Storage []StorageSummary `json:"storage"`
}

func (t *tools) listStorage(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listStorageOut, error) {
	if err := core.Require(ctx, store.ScopeRead); err != nil {
		return nil, listStorageOut{}, err
	}
	ts, err := t.c.ListBackupTargets(ctx)
	if err != nil {
		return nil, listStorageOut{}, err
	}
	out := listStorageOut{Storage: make([]StorageSummary, len(ts))}
	for i, s := range ts {
		out.Storage[i] = StorageSummary{ID: s.ID, Name: s.Name, Kind: string(s.Kind), Encrypted: s.Encrypted()}
	}
	return nil, out, nil
}

type listBackupsIn struct {
	Database string `json:"database" jsonschema:"the service as project/service, or its ID"`
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
	Database string `json:"database" jsonschema:"the service to overwrite, as project/service or ID"`
	BackupID string `json:"backup_id" jsonschema:"backup to restore (see list_backups)"`
	Confirm  string `json:"confirm" jsonschema:"must equal the service name to confirm the data will be replaced"`
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
