---
title: Data browser
description: Browse PostgreSQL tables and Redis keys, and run queries, from the service page
order: 6
---

A database's **Data** tab shows what it holds without deploying pgAdmin, Adminer
or RedisInsight next to it. The manager runs the database's own client (`psql`,
`redis-cli`) inside its container, like backups do: nothing is exposed, nothing
is installed, and the client always matches the server. The database must be
running.

## Browse

- **PostgreSQL**: every table and view outside the system schemas, with its size
  and estimated row count. A table opens on its columns (types, primary key) and
  its rows, 50 per page, sorted by the primary key or any column you click.
  Filters compare a column to a value (`=`, `≠`, `<`, `≤`, `>`, `≥`), match a
  `like` pattern (case-insensitive, `%` as wildcard) or test for NULL.
  **CSV** downloads every row matching the filters, streamed (no size cap).
- **Redis**: keys are listed with `SCAN` (never `KEYS`), filtered by a glob
  pattern such as `user:*`, with their type, expiry and memory use. A key opens
  on its value whatever the type (string, hash, list, set, sorted set, stream),
  paged for big collections.
- Browsing never writes: queries run in a read-only session with a 5 s
  statement timeout (10 min for a CSV export).
- Long values are cut at 4 KiB in the grid (flagged with `…`); click a cell to see
  it whole, JSON indented. `bytea` columns show as hex (`\x…`).

## Console

*Console* runs SQL against PostgreSQL, or a command against Redis, and shows the
result (at most 200 rows or 1 MiB of output; add a `LIMIT`, or export the table).
*Ctrl+Enter* runs it.

- By default the console is **read-only**: the PostgreSQL session refuses writes,
  and Redis commands that change data or the server (`SET`, `DEL`, `CONFIG`…)
  are refused. It guards against mistakes; it is not a permission boundary.
- **Allow writes** (asks first) lifts that. On PostgreSQL the whole input then
  runs in **one transaction**: if a statement fails, nothing is applied. Only the
  last statement's result is shown.
- Statements time out after 30 s. Redis commands that block or never return
  (`MONITOR`, `SUBSCRIBE`, `BLPOP`, `XREAD BLOCK`…) are always refused: use the
  [terminal](/docs/services) and `redis-cli` for those.

## Access

Browsing (and CSV export) needs a **read** token; the console needs **admin**,
which the web UI always has. Every console run lands in the
[audit log](/docs/api) as `data console` or `data console (write)`, with the
service but without the query, which may hold secrets.

Endpoints, for scripts: `GET /api/services/{id}/data/tables`,
`GET /api/services/{id}/data/tables/{schema}/{table}` (`limit`, `offset`, `order`,
`desc`, `filters` as a JSON array of `{column, op, value}`), `…/export`,
`GET /api/services/{id}/data/keys` (`cursor`, `pattern`),
`GET /api/services/{id}/data/key` (`key`, `cursor`) and
`POST /api/services/{id}/data/console` (`{query, write}`).
