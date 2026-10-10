---
title: Data browser
description: Browse PostgreSQL tables and Redis keys, and run queries, from the service page
order: 6
---

A database's **Data** tab shows what it holds without deploying pgAdmin, Adminer
or RedisInsight next to it. The manager runs the database's own client (`psql`,
`redis-cli`) inside its container, like backups do: nothing is exposed, nothing
is installed, and the client always matches the server. The database must be
running. Agents get the same through [MCP](/docs/api) tools, with the same scopes.

MySQL, MariaDB and MongoDB services have no Data tab yet: open their
[terminal](/docs/services) and use their client with the credentials in the
container's environment: `mysql -uroot -p"$MYSQL_ROOT_PASSWORD" app` (`mariadb`
for MariaDB), or `mongosh -u "$MONGO_INITDB_ROOT_USERNAME" -p
"$MONGO_INITDB_ROOT_PASSWORD"`.

## Browse

- **PostgreSQL**: the tables and views outside the system schemas are listed on
  the left (the list can be hidden). When the instance holds several databases
  (your app created more with `CREATE DATABASE`), pick one at the top right of
  the tab; Browse and Console share it. The service's own database is the
  default. **+** creates a new, empty database owned by the service's user (admin
  only): apps reach it with the same credentials, ending the connection URL with
  its name. A table opens as a grid: each column shows
  its type, a key marks the primary key and `*` a required column. Rows come 25
  to 200 per page, sorted by the primary key or any column you click. *Search rows*
  matches text in any column. *Filter* adds a condition on one column: compare it
  to a value (`=`, `!=`, `<`, `<=`, `>`, `>=`), match an `ilike` pattern (`%` as
  wildcard) or test for NULL; ✓ or Enter applies it. The download button exports
  every row matching the search and filters as CSV, streamed (no size cap). The
  page count is approximate: it comes from Postgres' own row estimate.
- **Redis**: keys are listed with `SCAN` (never `KEYS`), grouped by their `:`
  segments (`user:42` under `user:`), with their type; a clock marks keys that
  expire. *Match keys* takes a word (matched anywhere) or a glob such as `user:*`.
  A key opens on its value whatever the type (string, hash, list, set, sorted
  set, stream), paged for big collections.
- Click a row to see it whole in a side panel, JSON indented, and copy a field or
  the row as JSON. Values longer than 4 KiB are cut (flagged with `…`).
- Browsing never writes: queries run in a read-only session with a 5 s
  statement timeout (10 min for a CSV export).

## Console

*Console* runs SQL against PostgreSQL, or a command against Redis, and shows the
result (at most 200 rows or 1 MiB of output; add a `LIMIT`, or export the table).
*Ctrl+Enter* runs it. The SQL editor completes keywords and your tables' and
columns' names; results can be copied as CSV or JSON. The Redis console works like
`redis-cli`: a transcript of commands and replies, ↑ for past commands. Past
queries are kept per database in this browser only (*History*).

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
