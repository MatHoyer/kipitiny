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

- **Read** (any token): `list_projects` (empty ones too, with their git link), `list_services`, `get_app_status`, `get_logs`, `get_deployment_log`, `get_project_compose`, `get_project_git`, `list_backups`, `list_storage`.
- **Deploy**: `deploy`, `deploy_image` (admin, or a deploy token changing an existing app's tag), `rollback`, `service_action` (start, stop, restart), `backup_database`.
- **Admin**: `create_project`, `rename_project`, `rename_service`, `set_project_env`, `apply_project_compose`, `link_project_git` and `sync_project_git` (both with a `dry_run` preview), `restore_database` (requires the database name as confirmation).

Deleting projects or services stays in the UI. Secrets are masked in every response. Every
mutation, from the UI, a token or an agent (including refused attempts), lands
in the audit log (kept 90 days).
