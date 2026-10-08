---
title: API tokens, MCP and audit
description: Scoped tokens, the built-in MCP endpoint for agents, and the audit log
order: 11
---

Create tokens in *Settings*. Scopes: **read** (status, logs, backup lists),
**deploy** (plus deploy, including another tag of an app, rollback, start/stop, back up; see [Deploy from CI](/docs/deploys#deploy-from-ci)) and **admin**
(everything, including settings, revealed secrets and restores). Tokens work as
`Authorization: Bearer kpt_…` on `/api` and on the built-in **MCP** endpoint
(Streamable HTTP):

```sh
claude mcp add --transport http kipitiny https://kipitiny.example.com/mcp \
  --header "Authorization: Bearer kpt_…"
```

Tools are task-oriented rather than a copy of the REST API: `list_services`,
`get_app_status`, `get_logs`, `deploy`, `deploy_image` (admin, or a deploy token changing an existing app's tag), `rollback`,
`backup_database`, `list_backups`, `list_storage`, `restore_database` (admin, requires the
database name as confirmation). Secrets are masked in every response. Every
mutation, from the UI, a token or an agent (including refused attempts), lands
in the audit log (kept 90 days).
