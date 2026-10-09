---
title: API tokens, MCP and audit
description: Scoped tokens, the built-in MCP endpoint for agents, and the audit log
order: 12
---

Create tokens in *Settings*. Scopes: **read** (status, logs, backup lists),
**deploy** (plus deploy, including another tag of an app, rollback, start/stop, back up; see [Deploy from CI](/docs/deploys#deploy-from-ci)) and **admin**
(everything, including settings, revealed secrets, restores and the
[data console](/docs/data#console)). A read token can also browse database contents. Tokens work as
`Authorization: Bearer kpt_…` on `/api` and on the built-in **MCP** endpoint
(Streamable HTTP):

```sh
claude mcp add --transport http kipitiny https://kipitiny.example.com/mcp \
  --header "Authorization: Bearer kpt_…"
```

Tools are task-oriented rather than a copy of the REST API:

- **Read** (any token): `get_manager_status` (version, update, resource use), `get_topology`, `list_projects` (empty ones too, with their git link), `list_services`, `get_app_status`, `get_logs`, `list_deployments`, `get_deployment_log`, `get_project_compose`, `get_project_git`, `list_git_repos` and `list_git_branches` (to pick what to link), `list_backups`, `list_restores`, `list_backup_schedules`, `list_storage`, and the [data browser](/docs/data): `list_databases`, `list_tables`, `read_table`, `scan_redis_keys`, `get_redis_key`.
- **Deploy**: `deploy`, `deploy_image` (admin, or a deploy token changing an existing app's tag), `rollback`, `service_action` (start, stop, restart), `backup_database` (`database_name` picks one of a PostgreSQL instance's databases), `backup_project`, `verify_backup` (restore test).
- **Admin**: `create_project`, `rename_project`, `rename_service`, `delete_service` and `delete_project` (the name as confirmation; backups are kept), `set_project_env`, `delete_backup`, `set_backup_schedule` and `delete_backup_schedule`, `set_uptime_check` and `delete_uptime_check`, `apply_project_compose`, `link_project_git` and `sync_project_git` (both with a `dry_run` preview), `restore_database` (requires the database name as confirmation), `create_database`, `query_database` (the [console](/docs/data#console): read-only unless `write` is set), `get_connection` (reveals credentials), `get_audit_log`.

Secrets are masked in every response, except what `get_connection` reveals. Every
mutation, from the UI, a token or an agent (including refused attempts), lands
in the audit log (kept 90 days).
