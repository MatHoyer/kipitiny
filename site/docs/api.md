---
title: MCP server, API tokens and audit
description: Connect AI agents like Claude Code to kipitiny over MCP, with restricted API tokens and an audit log
order: 14
---

Create tokens in *Settings*. A token does what you may, or only what you
[restrict it to](#restricted-tokens): for CI, give it *deploy* on the projects
it ships (see [Deploy from CI](/docs/deploys#deploy-from-ci)). Tokens work as
`Authorization: Bearer kpt_…` on `/api` and on the built-in **MCP** endpoint
(Streamable HTTP):

```sh
claude mcp add --transport http kipitiny https://kipitiny.example.com/mcp \
  --header "Authorization: Bearer kpt_…"
```

Tools are task-oriented rather than a copy of the REST API:

- **Read** (*view*): `get_manager_status` (version, update, resource use), `get_topology`, `list_projects` (empty ones too, with their git link), `list_services`, `get_app_status`, `get_logs`, `list_deployments`, `get_deployment_log`, `get_project_compose`, `get_project_git`, `list_git_repos` and `list_git_branches` (to pick what to link), `list_backups`, `list_restores`, `list_backup_schedules`, `list_storage`, `list_previews`, the [data browser](/docs/data): `list_databases`, `list_tables`, `read_table`, `find_documents`, `scan_redis_keys`, `get_redis_key`, and the [files](/docs/files) of volumes and containers: `list_volume_files`.
- **Deploy** (*deploy*, *backups* for backups): `deploy`, `deploy_image` (*settings*, or only *deploy* to change an existing app's tag), `rollback`, `service_action` (start, stop, restart), `backup_database` (`database_name` picks one of a PostgreSQL instance's databases), `backup_project`, `verify_backup` (restore test), `deploy_preview` and `close_preview` ([previews](/docs/previews)).
- **Changes**, each needing its permission (*settings*, *env*, *delete*, *data*, *files*…; `sync_dns` and `get_audit_log` need an admin): `create_project`, `rename_project`, `rename_service`, `delete_service` and `delete_project` (the name as confirmation; backups are kept), `set_project_env`, `delete_backup`, `set_backup_schedule` and `delete_backup_schedule`, `set_uptime_check` and `delete_uptime_check`, `set_maintenance` (the [maintenance page](/docs/domains#maintenance-page) and mode), `set_sleep` ([sleep mode](/docs/sleep)), `sync_dns` (syncs [Cloudflare DNS](/docs/domains#cloudflare-dns) now), `apply_project_compose`, `link_project_git` and `sync_project_git` (both with a `dry_run` preview), `restore_database` (requires the database name as confirmation), `create_database`, `query_database` (the [console](/docs/data#console): read-only unless `write` is set), `get_connection` (reveals credentials), `get_audit_log`, and [files](/docs/files): `read_volume_file`, then in an app's volumes or container `write_volume_file` (text, or base64 up to 16 MiB), `make_volume_dir`, `move_volume_path` (or copy), `delete_volume_paths`.

A token acts for the user who created it: a [member's](/docs/users) token only
reaches the projects and does what that member may, and a tool needing a
permission it lacks is refused with its name.

## Restricted tokens

By default a token can do everything you can. Pick **Restricted** when creating
it to give it only some projects, permissions and server features, with the same
choices as a [member's access](/docs/users): for example an agent that may
deploy one project but never browse its databases. A restricted token acts as
a member, even when an admin created it, so it
never reaches users, settings or the audit log.

You can't give a token what you don't have yourself: those choices are disabled.
If you later lose a permission, your restricted tokens lose it too.

Secrets are masked in every response, except what `get_connection` reveals. Every
mutation, from the UI, a token or an agent (including refused attempts), lands
in the audit log (kept 90 days).

## Dry-run mode

Add `?dry_run=true` to the endpoint to let an agent rehearse without changing
anything, for example to review its plan before giving it the real endpoint:

```sh
claude mcp add --transport http kipitiny-dry "https://kipitiny.example.com/mcp?dry_run=true" \
  --header "Authorization: Bearer kpt_…"
```

Read tools work as usual, and so does `query_database` without `write`. Tools
that take `dry_run` (`apply_project_compose`, `install_template`,
`link_project_git`, `sync_project_git`) always return their plan. Every other
tool that changes something is not run: it answers with the arguments it would
have been called with. Those calls don't check scope, permissions or whether
their targets exist.
