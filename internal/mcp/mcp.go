// Package mcp exposes task-oriented tools to AI agents over the Model
// Context Protocol (Streamable HTTP at /mcp). Tools are thin adapters over
// the core layer; each checks the caller's permissions, masks secrets like
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
	"strconv"
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
// core.Actor in the request context. /mcp?dry_run=true serves the same tools
// in dry-run mode (see addTool).
func Handler(c *core.Core, version string) http.Handler {
	live, dry := newServer(c, version, false), newServer(c, version, true)
	return mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		if on, _ := strconv.ParseBool(r.URL.Query().Get("dry_run")); on {
			return dry
		}
		return live
	}, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
	})
}

func newServer(c *core.Core, version string, dryRun bool) *mcp.Server {
	docsIndex, docsBase, docsSources := docs(version)
	instructions := "Manage apps and databases (PostgreSQL, Redis, MySQL, MariaDB, MongoDB) on this kipitiny server. " +
		"Refer to services as project/service. A token may do what its owner may, or only the projects and permissions it was restricted to; " +
		"a refusal names the missing permission (with deploy but not settings, deploy_image only changes the tag of an existing app). Deleting a service or project or restoring a backup takes its name as an explicit confirmation. " +
		"Documentation of this version, as markdown: " + docsIndex + " lists every page; read the relevant one before guessing how a feature works. " +
		"If the site is unreachable, the same pages are in " + docsSources + "."
	if dryRun {
		instructions += " This connection is in dry-run mode: read tools work, tools taking dry_run always return their plan, " +
			"and every other tool that changes something only says it was not run. Nothing changes."
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "kipitiny", Version: version}, &mcp.ServerOptions{Instructions: instructions})
	s := toolset{server: server, dryRun: dryRun}
	t := &tools{c: c}
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}
	yes := true

	addTool(s, &mcp.Tool{Name: "list_services", Annotations: readOnly,
		Description: "List every project with its services, their kind, image, domain, tags and current status."}, t.listServices)
	addTool(s, &mcp.Tool{Name: "list_projects", Annotations: readOnly,
		Description: "List every project, including empty ones, with its server, tags, service names and the git compose file it follows, if any."}, t.listProjects)
	addTool(s, &mcp.Tool{Name: "get_manager_status", Annotations: readOnly,
		Description: "The manager's version and available update, its Docker engine, its database size, and the current CPU, memory and network use of every running service."}, t.getManagerStatus)
	addTool(s, &mcp.Tool{Name: "get_server_status", Annotations: readOnly,
		Description: "Each server's connection and Docker engine, its host's current CPU, load, memory, swap, disk (of Docker's data) and uptime, and for admins its last security check (firewall, SSH, updates, reboot needed) with hints. Use it when services are slow or a deploy fails for lack of space."}, t.getServerStatus)
	addTool(s, &mcp.Tool{Name: "get_cleanup", Annotations: readOnly,
		Description: "The disk cleanup's settings (what it removes from every server: unused images, volumes, build cache, stopped containers, old deployments), its schedule, whether one is running and the last run's result with the space reclaimed. Needs admin."}, t.getCleanup)
	addTool(s, &mcp.Tool{Name: "run_cleanup",
		Description: "Free disk space on every server now with the cleanup's saved settings (see get_cleanup), even if its schedule is off. It runs in the background; get_cleanup shows its progress and result. Database and app volumes, each service's image and those of its recent deployments are always kept. Needs admin."}, t.runCleanup)
	addTool(s, &mcp.Tool{Name: "get_topology", Annotations: readOnly,
		Description: "The network map of each server: entrypoints and the reverse proxy, Docker networks with subnets, and each project's containers with their IPs and aliases."}, t.getTopology)
	addTool(s, &mcp.Tool{Name: "get_audit_log", Annotations: readOnly,
		Description: "Recent mutations by users, tokens and agents, including refused ones: actor, action, target, status and error. Needs admin."}, t.getAuditLog)
	addTool(s, &mcp.Tool{Name: "create_project",
		Description: "Create an empty project on a server (this one by default). Add services with deploy_image, apply_project_compose or link_project_git."}, t.createProject)
	addTool(s, &mcp.Tool{Name: "rename_project",
		Description: "Rename a project. Its containers are renamed in place; nothing restarts."}, t.renameProject)
	addTool(s, &mcp.Tool{Name: "rename_service",
		Description: "Rename a service. Nothing restarts; until its next deploy it also answers to its old name on the project network. Renaming a database rewrites the {{ db.NAME.* }} references to it and redeploys the apps using it. Not for projects linked to git (edit the file)."}, t.renameService)
	addTool(s, &mcp.Tool{Name: "set_project_tags", Annotations: &mcp.ToolAnnotations{IdempotentHint: true},
		Description: "Replace a project's tags, which group projects in the UI. Tags are lowercased; up to 10, of up to 32 letters, digits, spaces and . _ / : -."}, t.setProjectTags)
	addTool(s, &mcp.Tool{Name: "set_service_tags", Annotations: &mcp.ToolAnnotations{IdempotentHint: true},
		Description: "Replace a service's tags, which group services in the UI; same rules as set_project_tags. " +
			"Not for projects linked to git (set x-kipitiny.tags in the file)."}, t.setServiceTags)
	addTool(s, &mcp.Tool{Name: "delete_service", Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes},
		Description: "Delete a service: its containers, volumes (data) and deploy logs; backups are kept. confirm must repeat the service name. " +
			"A database still referenced by an app, or a service of a git-linked project, is refused."}, t.deleteService)
	addTool(s, &mcp.Tool{Name: "delete_project", Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes},
		Description: "Delete a project with every service, volume and its network; backups are kept. confirm must repeat the project name."}, t.deleteProject)
	addTool(s, &mcp.Tool{Name: "get_project_git", Annotations: readOnly,
		Description: "A project's git link: repository, branch, compose file path, auto sync, the last sync's commit, time, error and warnings, and the services running another image than the file's."}, t.getProjectGit)
	addTool(s, &mcp.Tool{Name: "link_project_git", Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes},
		Description: "Link a project to a compose file in a git repository (see " + docsBase + "/compose/llms.txt). The file then owns the services: every commit is applied and apps it doesn't list are deleted with their volumes. " +
			"Run with dry_run first to see what the first sync changes."}, t.linkProjectGit)
	addTool(s, &mcp.Tool{Name: "sync_project_git", Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes},
		Description: "Apply a git-linked project's compose file now, as a push would. dry_run returns the plan only."}, t.syncProjectGit)
	addTool(s, &mcp.Tool{Name: "list_git_repos", Annotations: readOnly,
		Description: "Repositories the connected git providers can read (full name, clone URL, default branch), to pick one for link_project_git."}, t.listGitRepos)
	addTool(s, &mcp.Tool{Name: "list_git_branches", Annotations: readOnly,
		Description: "Branches of a repository a git provider can read."}, t.listGitBranches)
	addTool(s, &mcp.Tool{Name: "service_action",
		Description: "Start, stop or restart a service. A stopped service stays stopped until started."}, t.serviceAction)
	addTool(s, &mcp.Tool{Name: "get_deployment_log", Annotations: readOnly,
		Description: "The log of a deployment (pull, readiness, errors): a service's latest one, or one by ID from get_app_status."}, t.getDeploymentLog)
	addTool(s, &mcp.Tool{Name: "get_app_status", Annotations: readOnly,
		Description: "Status of one service: containers and health, settings (secrets masked), the last deployments with errors, current CPU/memory/network use, uptime check results."}, t.getAppStatus)
	addTool(s, &mcp.Tool{Name: "get_logs", Annotations: readOnly,
		Description: "Recent log lines of a service's running containers, optionally filtered by a case-insensitive substring."}, t.getLogs)
	addTool(s, &mcp.Tool{Name: "deploy",
		Description: "Deploy a service's current settings (zero-downtime for apps without published ports), or another tag of an app's image. Returns the deployment; poll get_app_status for the outcome."}, t.deploy)
	addTool(s, &mcp.Tool{Name: "deploy_image",
		Description: "Create (or update) an app running a Docker image in a project, then deploy it. The project is created if missing. With the deploy permission but not settings, only an existing app's image tag or digest can change."}, t.deployImage)
	addTool(s, &mcp.Tool{Name: "set_project_env",
		Description: "Set or remove a project's shared variables (readable) and secrets (write-only). Services use either as {{ project.NAME }} in an env value; they pick up changes on their next deploy."}, t.setProjectEnv)
	addTool(s, &mcp.Tool{Name: "get_project_compose", Annotations: readOnly,
		Description: "A project (or one of its services) as an equivalent docker-compose file, kipitiny settings in x-kipitiny blocks. Secret values are ${NAME} variables without values."}, t.getProjectCompose)
	addTool(s, &mcp.Tool{Name: "apply_project_compose", Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes},
		Description: "Make a project match a docker-compose file (format of get_project_compose, fully documented at " + docsBase + "/compose/llms.txt): creates and updates services, then deploys the changed ones. " +
			"dry_run returns the plan only. prune also deletes the apps the file doesn't list (with their volumes): confirm must repeat the project name."}, t.applyProjectCompose)
	addTool(s, &mcp.Tool{Name: "list_templates", Annotations: readOnly,
		Description: "One-click apps this kipitiny can install: each one's id, description, the inputs it asks for and its compose file."}, t.listTemplates)
	addTool(s, &mcp.Tool{Name: "install_template",
		Description: "Install a template (see list_templates) into a project, created if missing: applies its compose file with values as the inputs, then deploys. " +
			"Nothing existing is deleted. dry_run returns the plan only."}, t.installTemplate)
	addTool(s, &mcp.Tool{Name: "rollback",
		Description: "Redeploy the image of an earlier successful deployment (the previous one by default)."}, t.rollback)
	addTool(s, &mcp.Tool{Name: "backup_database",
		Description: "Start a backup of a service to a storage (local disk by default; see list_storage): a pg_dump of a PostgreSQL service, a dump of a MySQL, MariaDB or MongoDB one by its own tool, an archive of the volumes of a Redis service or an app with volumes."}, t.backupDatabase)
	addTool(s, &mcp.Tool{Name: "list_backups", Annotations: readOnly,
		Description: "Backups of a service with status, size, storage (targetId) and restore-test result."}, t.listBackups)
	addTool(s, &mcp.Tool{Name: "list_storage", Annotations: readOnly,
		Description: "Where kipitiny can store files (local disk, S3, drives), with their IDs and whether files are encrypted."}, t.listStorage)
	addTool(s, &mcp.Tool{Name: "restore_database", Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes},
		Description: "Replace a service's data with a backup. Destructive: confirm must repeat the service name. For a PostgreSQL dump, apps referencing the database are stopped meanwhile; for a volume archive, the service itself is."}, t.restoreDatabase)

	addTool(s, &mcp.Tool{Name: "backup_project",
		Description: "Back up every database and app with volumes of a project to one storage. Services whose backup can't start are listed in error; the others still run."}, t.backupProject)
	addTool(s, &mcp.Tool{Name: "verify_backup",
		Description: "Restore-test a successful backup in a throwaway, network-less container; list_backups then shows the result."}, t.verifyBackup)
	addTool(s, &mcp.Tool{Name: "delete_backup", Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes},
		Description: "Delete a backup and its file from storage."}, t.deleteBackup)
	addTool(s, &mcp.Tool{Name: "get_analytics", Annotations: readOnly,
		Description: "Cookieless web analytics of an app: pageviews, visitors, visits, bounces, time on site, daily series, " +
			"and top pages, referrers, UTM, browsers, OS, devices, countries, custom events and identified users; filters narrow it to a value. With tracker instead of service, a pixel's views or a link's clicks."}, t.getAnalytics)
	addTool(s, &mcp.Tool{Name: "list_trackers", Annotations: readOnly,
		Description: "Pixels (1x1 GIFs counting views, for emails and pages without the script) and tracked links (redirects counting " +
			"clicks), with their URL and last 30 days' hits. Needs admin and analytics on."}, t.listTrackers)
	addTool(s, &mcp.Tool{Name: "create_tracker",
		Description: "Create a pixel or a tracked link on the analytics domain; returns its URL. Needs admin."}, t.createTracker)
	addTool(s, &mcp.Tool{Name: "delete_tracker", Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes},
		Description: "Delete a pixel or a tracked link and its counted hits; its URL stops counting (a link then answers 404). Needs admin."}, t.deleteTracker)
	addTool(s, &mcp.Tool{Name: "list_visits", Annotations: readOnly,
		Description: "Recent visits of an app with analytics on, newest first: pages and events counts, entry/exit, referrer, UTM, " +
			"country, browser, OS, device, and the identity the app gave (kipitiny.identify). A visitor without identity is only " +
			"recognized within a day."}, t.listVisits)
	addTool(s, &mcp.Tool{Name: "get_visit", Annotations: readOnly,
		Description: "One visit of an app: its journey (pages in order with times, custom events with their properties)."}, t.getVisit)
	addTool(s, &mcp.Tool{Name: "list_restores", Annotations: readOnly,
		Description: "Restores of a service: which backup, when and the outcome."}, t.listRestores)
	addTool(s, &mcp.Tool{Name: "list_deployments", Annotations: readOnly,
		Description: "A service's deployments, newest first: image, status, error, commit and who triggered it. IDs work with get_deployment_log and rollback."}, t.listDeployments)
	addTool(s, &mcp.Tool{Name: "list_backup_schedules", Annotations: readOnly,
		Description: "A service's backup schedules: cron (UTC), storage, retention, restore tests and next run."}, t.listBackupSchedules)
	addTool(s, &mcp.Tool{Name: "set_backup_schedule",
		Description: "Add a backup schedule to a service, or change one (schedule_id; omitted fields are kept). Each schedule prunes its own backups by its retention."}, t.setBackupSchedule)
	addTool(s, &mcp.Tool{Name: "delete_backup_schedule",
		Description: "Delete one of a service's backup schedules; the backups it made are kept."}, t.deleteBackupSchedule)
	addTool(s, &mcp.Tool{Name: "list_jobs", Annotations: readOnly,
		Description: "An app's scheduled jobs: command, cron (UTC), timeout, next run and the last run's outcome."}, t.listJobs)
	addTool(s, &mcp.Tool{Name: "set_job",
		Description: "Add a scheduled job to an app, or change one by name (omitted fields are kept): a shell command run on a cron schedule in a one-off container of the app's deployed image, with its env, networks and volumes. A run that exits non-zero or times out notifies job.failed."}, t.setJob)
	addTool(s, &mcp.Tool{Name: "delete_job",
		Description: "Delete one of an app's scheduled jobs, its run history and logs; a run in progress is stopped."}, t.deleteJob)
	addTool(s, &mcp.Tool{Name: "run_job",
		Description: "Run one of an app's jobs now, in the background (refused while it runs); list_job_runs shows the outcome and log. Needs deploy."}, t.runJob)
	addTool(s, &mcp.Tool{Name: "list_job_runs", Annotations: readOnly,
		Description: "A job's last runs, newest first (status, exit code, error, who ran it), with the log of the latest run or of run_id."}, t.listJobRuns)
	addTool(s, &mcp.Tool{Name: "set_uptime_check",
		Description: "Create or replace the uptime check of an app with a public domain: an HTTPS request to a path at an interval, notifying when it goes down. It runs right away; get_app_status shows its results. A git project's checks are in its compose file (x-kipitiny.uptime) instead."}, t.setUptimeCheck)
	addTool(s, &mcp.Tool{Name: "delete_uptime_check",
		Description: "Remove a service's uptime check and its history. Not for a git project: remove x-kipitiny.uptime from its file."}, t.deleteUptimeCheck)
	addTool(s, &mcp.Tool{Name: "set_maintenance",
		Description: "Set a project's maintenance page, shown (503 with Retry-After) while one of its apps has no replica to answer, and maintenance mode, which shows it to every visitor of its apps except allowed IPs while the replicas keep running (migrations, manual work); also the starting page visitors get while the project wakes from sleep. Applied within seconds, nothing restarts; works on git-linked projects too."}, t.setMaintenance)
	addTool(s, &mcp.Tool{Name: "list_previews", Annotations: readOnly,
		Description: "A project's preview environments: name, branch, commit, last deploy, time to live and the URLs of their apps."}, t.listPreviews)
	addTool(s, &mcp.Tool{Name: "deploy_preview",
		Description: "Create or update a preview environment of a project: a copy with fresh databases, its apps on the preview base domain (asleep when unused), the project variables of its preview settings instead of production's secrets, and the given image tags. Changed services deploy in the background; list_previews and get_app_status show the outcome. Refused when a value it needs is missing or points at production. Needs deploy."}, t.deployPreview)
	addTool(s, &mcp.Tool{Name: "close_preview", Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes},
		Description: "Close a preview environment: delete its containers, volumes and databases. Needs deploy."}, t.closePreview)
	addTool(s, &mcp.Tool{Name: "get_preview_settings", Annotations: readOnly,
		Description: "A project's preview settings (secrets masked): the base domain of previews' apps, the preview variables and secrets replacing production's, the production secrets previews still inherit, the time to live, sleep delay and whether seeding with production data is allowed."}, t.getPreviewSettings)
	addTool(s, &mcp.Tool{Name: "set_preview_settings",
		Description: "Change a project's preview settings (see get_preview_settings); omitted fields are kept. Previews pick them up on their next deploy_preview. Needs env."}, t.setPreviewSettings)
	addTool(s, &mcp.Tool{Name: "set_sleep",
		Description: "Set a project's sleep mode: after this long without a request to any of its apps, every service stops, databases too, and the next request starts them again behind a \"starting\" page (503 with Retry-After for API calls). Background workers don't run while it sleeps; a deploy, a start, a backup or a data query wakes it. Needs an app with a domain, and no service with published ports or the host network; get_app_status reports sleeping services."}, t.setSleep)
	addTool(s, &mcp.Tool{Name: "list_domains", Annotations: readOnly,
		Description: "The base domains added to kipitiny, under which apps get their domain (app.example.com under example.com): whether a connected Cloudflare token manages their DNS, its SSL/TLS mode, and whether records are proxied."}, t.listDomains)
	addTool(s, &mcp.Tool{Name: "sync_dns", Annotations: &mcp.ToolAnnotations{IdempotentHint: true},
		Description: "Sync the Cloudflare DNS records and tunnel routes of service domains now instead of waiting for the next round (every ten minutes, and seconds after a domain changes); public IPs are detected again. Returns each managed hostname's state. Needs admin and a connected Cloudflare token."}, t.syncDNS)
	addTool(s, &mcp.Tool{Name: "list_networks", Annotations: readOnly,
		Description: "Networks created by hand on each server, next to the automatic proxy and project networks, with the services on them and the name each is reached at (<project>-<service>)."}, t.listNetworks)
	addTool(s, &mcp.Tool{Name: "create_network",
		Description: "Create a network on a server (this one by default) that services of several projects can join with set_service_networks to reach each other."}, t.createNetwork)
	addTool(s, &mcp.Tool{Name: "delete_network", Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes},
		Description: "Delete a network created by hand. Its services leave it right away (nothing restarts) and can no longer reach each other through it."}, t.deleteNetwork)
	addTool(s, &mcp.Tool{Name: "set_service_networks",
		Description: "Set the networks created by hand that a service joins, besides its project network; applied to its containers right away, without restarting them. Works on git-linked projects too."}, t.setServiceNetworks)

	addTool(s, &mcp.Tool{Name: "list_databases", Annotations: readOnly,
		Description: "The databases of a PostgreSQL, MySQL, MariaDB or MongoDB service's instance, with their size; main is the service's own."}, t.listDatabases)
	addTool(s, &mcp.Tool{Name: "create_database",
		Description: "Create a database in a PostgreSQL, MySQL or MariaDB service's instance, owned by the service's user, or in a MongoDB one with its first collection."}, t.createDatabase)
	addTool(s, &mcp.Tool{Name: "list_tables", Annotations: readOnly,
		Description: "Tables and views of a PostgreSQL, MySQL or MariaDB database with their columns, estimated rows and size, or the collections of a MongoDB one (no columns)."}, t.listTables)
	addTool(s, &mcp.Tool{Name: "read_table", Annotations: readOnly,
		Description: "Rows of a PostgreSQL, MySQL or MariaDB table or view, paged, sorted and filtered. Cells are text (null is NULL, binary is 0x hex); long ones are cut and listed in truncated."}, t.readTable)
	addTool(s, &mcp.Tool{Name: "find_documents", Annotations: readOnly,
		Description: "Documents of a MongoDB collection as relaxed Extended JSON, paged, filtered and sorted; long ones are cut and listed in truncated."}, t.findDocuments)
	addTool(s, &mcp.Tool{Name: "scan_redis_keys", Annotations: readOnly,
		Description: "One SCAN step over a Redis service's keys, with their type, TTL and size. Call again with the returned cursor until it is 0."}, t.scanRedisKeys)
	addTool(s, &mcp.Tool{Name: "get_redis_key", Annotations: readOnly,
		Description: "One page of a Redis key's value: [value] for a string, [field, value] pairs for a hash, [member, score] for a sorted set."}, t.getRedisKey)
	addTool(s, &mcp.Tool{Name: "query_database", Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes},
		Description: "Run SQL on a PostgreSQL, MySQL or MariaDB service, a command on a Redis one, or mongosh JavaScript on a MongoDB one; read-only unless write is set. Prefer read_table, find_documents and get_redis_key to look at data."}, t.queryDatabase)
	addTool(s, &mcp.Tool{Name: "get_connection",
		Description: "A database service's host, port, user, password and URL, as apps of its project reach it."}, t.getConnection)

	addTool(s, &mcp.Tool{Name: "list_volume_files", Annotations: readOnly,
		Description: "Files and folders in a service's volumes (an app's named volumes, a database's data volume) or in its running container: name, type, size, modified time, mode, owner, symlink target. " +
			"Paths are <volume>/<path>, or @container/<absolute path> for the container's own filesystem (the first running replica; @<container ID>/... picks one); an empty path lists the volumes and @container. " +
			"A database's files are read-only. Changes in a container's filesystem reach that replica only and are lost on the next deploy: keep files that matter in a volume."}, t.listVolumeFiles)
	addTool(s, &mcp.Tool{Name: "read_volume_file", Annotations: readOnly,
		Description: "The content of a text file in a service's volumes or container (see list_volume_files for paths), up to 1 MiB; binary files are refused. Files may hold secrets."}, t.readVolumeFile)
	addTool(s, &mcp.Tool{Name: "write_volume_file",
		Description: "Write a file in an app's volumes or container (see list_volume_files for paths; a container's changes are lost on the next deploy), as text or base64 up to 16 MiB (e.g. a plugin's .jar), creating missing folders. New files belong to their folder's owner. " +
			"The app sees it right away; most apps need a restart (service_action) to load new plugins or config."}, t.writeVolumeFile)
	addTool(s, &mcp.Tool{Name: "make_volume_dir",
		Description: "Create a folder, with missing parents, in an app's volumes or container."}, t.makeVolumeDir)
	addTool(s, &mcp.Tool{Name: "move_volume_path",
		Description: "Move, rename or (with copy) copy a file or folder within an app's volumes, or within its container."}, t.moveVolumePath)
	addTool(s, &mcp.Tool{Name: "delete_volume_paths", Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes},
		Description: "Delete files or folders (with their content) in an app's volumes or container; nothing is deleted unless every path exists. Back up first (backup_database) when unsure."}, t.deleteVolumePaths)

	return server
}

type tools struct{ c *core.Core }

// mutate checks there is an actor, runs f and records the outcome in the audit log.
func mutate[T any](ctx context.Context, c *core.Core, action, target string, f func() (T, error)) (T, error) {
	var zero T
	if err := core.Authenticated(ctx); err != nil {
		c.Audit(ctx, "mcp "+action, target, http.StatusForbidden, err) // denied attempts matter too
		return zero, err
	}
	out, err := f()
	status := http.StatusOK
	if errors.Is(err, core.ErrForbidden) {
		status = http.StatusForbidden
	} else if err != nil {
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
	Project string   `json:"project"`
	Service string   `json:"service"`
	Kind    string   `json:"kind"`
	Image   string   `json:"image"`
	Domain  string   `json:"domain,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	Status  string   `json:"status"`
}

type listServicesOut struct {
	Services []ServiceSummary `json:"services"`
}

func (t *tools) listServices(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listServicesOut, error) {
	if err := core.Authenticated(ctx); err != nil {
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
				Domain: s.Domain, Tags: s.Tags, Status: status(s),
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
	if err := core.Authenticated(ctx); err != nil {
		return nil, appStatusOut{}, err
	}
	svc, err := t.service(ctx, in.Service, core.PermView)
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
	if err := core.Authenticated(ctx); err != nil {
		return nil, getLogsOut{}, err
	}
	svc, err := t.service(ctx, in.Service, core.PermView)
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
	dep, err := mutate(ctx, t.c, "deploy", in.Service, func() (store.Deployment, error) {
		svc, err := t.service(ctx, in.Service, core.PermDeploy)
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

// deployImage needs the settings permission to create apps or change anything
// but the tag: it could otherwise run any image with env that references
// password-manager and database secrets, publish host ports and claim domains.
// The deploy permission gets what REST deploy allows, another tag of an
// existing app's image.
func (t *tools) deployImage(ctx context.Context, _ *mcp.CallToolRequest, in deployImageIn) (*mcp.CallToolResult, store.Deployment, error) {
	dep, err := mutate(ctx, t.c, "deploy_image", in.Project+"/"+in.Name, func() (store.Deployment, error) {
		project, err := t.findProject(ctx, in.Project)
		if err == nil && core.Authorize(ctx, project.ID, core.PermSettings) != nil {
			return t.retag(ctx, in)
		}
		if errors.Is(err, store.ErrNotFound) {
			project, err = t.c.CreateProject(ctx, in.Project, "")
		}
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

// retag is deploy_image without the settings permission: the app must exist
// and only its image tag or digest may change.
func (t *tools) retag(ctx context.Context, in deployImageIn) (store.Deployment, error) {
	svc, err := t.service(ctx, in.Project+"/"+in.Name, core.PermDeploy)
	if errors.Is(err, store.ErrNotFound) {
		return store.Deployment{}, fmt.Errorf("%w: needs the settings permission to create an app", core.ErrForbidden)
	}
	if err != nil {
		return store.Deployment{}, err
	}
	if svc.Kind != store.ServiceKindApp {
		return store.Deployment{}, fmt.Errorf("%s/%s exists and is not an app", in.Project, in.Name)
	}
	if in.Env != nil || in.PublishedPorts != nil || in.StopGraceSeconds != nil ||
		(in.Domain != "" && in.Domain != svc.Domain) || (in.Port != 0 && in.Port != svc.Port) {
		return store.Deployment{}, fmt.Errorf("%w: needs the settings permission to change anything but the image tag", core.ErrForbidden)
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
	out, err := mutate(ctx, t.c, "set_project_env", in.Project, func() (setProjectEnvOut, error) {
		project, err := t.project(ctx, in.Project, core.PermEnv)
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
	if err := core.Authenticated(ctx); err != nil {
		return nil, getProjectComposeOut{}, err
	}
	project, err := t.project(ctx, in.Project, core.PermView)
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
	out, err := mutate(ctx, t.c, "apply_project_compose", in.Project, func() (core.ComposePlan, error) {
		project, err := t.project(ctx, in.Project, core.PermSettings)
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

// project finds a project by name and checks p on it.
func (t *tools) project(ctx context.Context, name string, p core.Perm) (store.Project, error) {
	pr, err := t.findProject(ctx, name)
	if err != nil {
		return store.Project{}, err
	}
	return pr, core.Authorize(ctx, pr.ID, p)
}

// service resolves a service (project/service or ID) and checks p on its
// project.
func (t *tools) service(ctx context.Context, ref string, p core.Perm) (store.Service, error) {
	return t.c.AuthorizeService(ctx, ref, p)
}

// backup checks p on a backup's project; the manager's own are admin only.
func (t *tools) backup(ctx context.Context, id string, p core.Perm) error {
	projectID, err := t.c.ProjectOf(ctx, core.IDBackup, id)
	if err != nil {
		return err
	}
	if projectID == "" {
		return core.RequireAdmin(ctx)
	}
	return core.Authorize(ctx, projectID, p)
}

type rollbackIn struct {
	Service      string `json:"service" jsonschema:"the service as project/service, or its ID"`
	DeploymentID string `json:"deployment_id,omitempty" jsonschema:"deployment to return to; default: the previous successful one"`
}

func (t *tools) rollback(ctx context.Context, _ *mcp.CallToolRequest, in rollbackIn) (*mcp.CallToolResult, store.Deployment, error) {
	dep, err := mutate(ctx, t.c, "rollback", in.Service, func() (store.Deployment, error) {
		svc, err := t.service(ctx, in.Service, core.PermDeploy)
		if err != nil {
			return store.Deployment{}, err
		}
		return t.c.Rollback(ctx, svc.ID, in.DeploymentID)
	})
	return nil, dep, err
}

type backupIn struct {
	Database string `json:"database" jsonschema:"the service to back up (a database or an app with volumes) as project/service, or its ID"`
	Storage  string `json:"storage,omitempty" jsonschema:"storage ID (see list_storage); default local disk"`
	// The service is "database" for compatibility; this picks inside it.
	DatabaseName string `json:"database_name,omitempty" jsonschema:"for PostgreSQL, which of the instance's databases to dump; default the service's own"`
}

func (t *tools) backupDatabase(ctx context.Context, _ *mcp.CallToolRequest, in backupIn) (*mcp.CallToolResult, store.Backup, error) {
	b, err := mutate(ctx, t.c, "backup_database", in.Database, func() (store.Backup, error) {
		svc, err := t.service(ctx, in.Database, core.PermBackups)
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
	if err := core.Authenticated(ctx); err != nil {
		return nil, listStorageOut{}, err
	}
	if err := core.RequireFeature(ctx, core.FeatureStorage); err != nil {
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
	if err := core.Authenticated(ctx); err != nil {
		return nil, listBackupsOut{}, err
	}
	svc, err := t.service(ctx, in.Database, core.PermView)
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
	r, err := mutate(ctx, t.c, "restore_database", in.Database, func() (store.Restore, error) {
		svc, err := t.service(ctx, in.Database, core.PermRestore)
		if err != nil {
			return store.Restore{}, err
		}
		return t.c.RestoreBackup(ctx, in.BackupID, svc.ID, in.Confirm)
	})
	return nil, r, err
}
