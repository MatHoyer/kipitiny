---
title: Data browser
description: Browse and edit tables, documents and keys, and run queries, from the service page
order: 6
---

Every database's **Data** tab shows what it holds without deploying pgAdmin,
Adminer, Compass or RedisInsight next to it. The manager runs the database's own
client (`psql`, `mysql`/`mariadb`, `mongosh`, `redis-cli`) inside its container,
like backups do: nothing is exposed, nothing is installed, and the client always
matches the server. The database must be running. An app with volumes gets the
tab too, for the [SQLite](#sqlite) files it keeps there. Agents get the same
through [MCP](/docs/api) tools, with the same permissions.

On MySQL, MariaDB and MongoDB, browsing and the read-only console use a
**read-only user** the manager creates in the database (`kipitiny_read`, with
`SELECT` on MySQL and MariaDB, `readAnyDatabase` on MongoDB; its password comes
from the root one). The server itself refuses writes there.

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
- **MySQL and MariaDB**: the same browser. The database picker lists the
  instance's databases (the server's own aside), **+** creates one the app user
  owns. Binary columns show as `0x…` hex; `ilike` is `LIKE` with the column's
  collation (usually case-insensitive).
- **MongoDB**: the collections and views of the picked database (the instance's
  `admin`, `config` and `local` aside). **+** creates a database with its first
  collection, which it needs to exist (MongoDB keeps no empty database). Documents show as indented
  [Extended JSON](https://www.mongodb.com/docs/manual/reference/mongodb-extended-json/),
  10 to 100 per page. *Filter* and *Sort* take a query and a sort as JSON
  (`{ "status": "active", "age": { "$gte": 18 } }`, `{ "createdAt": -1 }`),
  applied with Enter.
- **SQLite** (an app): the database picker lists the SQLite files found in the
  app's volumes, as `<volume>/<path>`. Then it's the same browser, with a few
  SQLite differences. Blobs show as `0x…` hex, `ilike` is SQLite's `LIKE`
  (case-insensitive for ASCII only), and the page count is unknown: SQLite keeps
  no row estimate. See [SQLite](#sqlite).
- **Redis**: keys are listed with `SCAN` (never `KEYS`), grouped by their `:`
  segments (`user:42` under `user:`), with their type; a clock marks keys that
  expire. *Match keys* takes a word (matched anywhere) or a glob such as `user:*`.
  A key opens on its value whatever the type (string, hash, list, set, sorted
  set, stream), paged for big collections.
- Click a row to see it whole in a side panel, JSON indented, and copy a field or
  the row as JSON. Values longer than 4 KiB are cut (flagged with `…`).
- Browsing never writes: queries run in a read-only session (or as the
  read-only user) with a 5 s timeout (10 min for a CSV export). To change data,
  allow writes (below).

## Edit

Browse is read-only until you switch it to **Allow writes** (it asks first; the
frame turns red, as the console's does). Changes then stay **pending** in your
browser, marked in the grid, until you save them:

- **PostgreSQL, MySQL, MariaDB and SQLite**: click a cell to edit it (Enter or leaving it
  keeps the text, Escape drops it, Tab moves to the next cell). An edited cell
  turns amber, its old value in its tooltip. **+ Row** adds a row at the top, in
  green, its cells on `DEFAULT` until you fill them. The bin marks a row for
  deletion: it shows struck through until you save, and ↶ takes it back. The
  expand button opens the row whole, to edit long values or set a field to
  `NULL` (or back to `DEFAULT` on a new row). Only a table with a primary key
  can be edited: it is how a row is found again. Views, values cut at 4 KiB and,
  on MySQL, MariaDB and SQLite, binary columns (SQLite: a declared type naming
  `BLOB`) are left to the console.
- **MongoDB**: the pencil opens a document as Extended JSON, as shown. Only the
  top-level fields you change are saved (`$set`, and `$unset` for those you
  remove), so the others keep their exact types (an `int64` stays one). **+
  Document** adds one; without an `_id`, MongoDB gives it one. The `_id` itself
  can't change: add a new document and delete the old one. Edited documents turn
  amber, with the fields that changed; new ones green; deleted ones struck
  through. Documents cut at 4 KiB, and views, are left to the console.
- **Redis**: a string's value is edited whole in place. A hash, list, set or
  sorted set opens as a grid, edited like a table: values, members and scores
  change in place, **+ Item** adds one (a list appends it), the bin deletes one.
  A hash field can't be renamed, nor a list item moved: add the new one and
  delete the old. A stream's entries can only be deleted. **Delete key** removes
  the whole key; edits keep its expiry. **+ Key** creates one (string, hash,
  list, set or sorted set) with its first items, without expiry; the save stops
  if the name is taken.
- **Save** (or *Ctrl+S*) first shows the statements the manager wrote for your
  changes (SQL, `mongosh` calls or Redis commands): deletes, then updates, then
  inserts. Nothing runs until you confirm. On PostgreSQL, MySQL, MariaDB and
  SQLite they run as **one transaction**, as the service's own user (root on MySQL and
  MariaDB): if one fails, nothing is saved and your changes stay pending, to fix
  and save again. On Redis they run in one script, atomically, after checking
  the values you changed. MongoDB has no transactions on a single server: every
  update and delete is checked first, then they run in order as root; if one
  still fails, the error says how many were saved before it.
- An update or delete must find its row as you loaded it: by its primary key (a
  document's `_id`), and for an update with the edited columns' (fields') values
  unchanged. If someone changed or deleted it in the meantime, the save stops
  rather than overwrite their change; reload and edit it again.
- **Tables and collections**: with writes allowed, **+** at the top of the list
  (or **+ Table** / **+ Collection** in an empty database) creates one. A table
  is designed column by column: a name, a type picked from the usual ones
  (grouped as numbers, text, date and time, other; *Custom…* takes any type the
  database knows), a default expression, primary key, NULL allowed. Its
  `CREATE TABLE` is reviewed before it runs. The bin in the toolbar drops the open table, view or collection,
  after reviewing the statement and typing its name. A drop never cascades: if
  a view or a foreign key depends on the table, the database refuses.
- Tables without a primary key, and views, can still be dropped with writes
  allowed; their rows are left to the console.
- Switching table, page, sort, filter, database or view, or leaving the page,
  asks before dropping pending changes. **Discard** drops them all.

## Console

*Console* runs SQL against PostgreSQL, MySQL, MariaDB and SQLite, JavaScript against
MongoDB (as in `mongosh`, `db` being the picked database), or a command against
Redis, and shows the result (at most 200 rows or 1 MiB of output; add a `LIMIT`,
or export the table).
*Ctrl+Enter* runs it. The SQL editor completes keywords and your tables' and
columns' names; results can be copied as CSV or JSON. The Redis console works like
`redis-cli`, and MongoDB's like `mongosh`: a transcript of commands and their
printout, ↑ for past commands. Past
queries are kept per database in this browser only (*History*). `psql`
meta-commands (`\dt`, `\!`…) don't run: query `information_schema` instead.

- By default the console is **read-only**: the PostgreSQL session refuses writes,
  a SQLite file is opened read-only, MySQL, MariaDB and MongoDB run as the
  read-only user, and Redis commands that
  change data or the server (`SET`, `DEL`, `CONFIG`…) are refused. It guards
  against mistakes; the admin can lift it.
- **Allow writes** (asks first) lifts that. On PostgreSQL and SQLite the whole
  input then runs in **one transaction**: if a statement fails, nothing is applied. MySQL,
  MariaDB and MongoDB then run as root (statements apply one by one). Only the
  last statement's result is shown.
- Statements time out after 30 s. Redis commands that block or never return
  (`MONITOR`, `SUBSCRIBE`, `BLPOP`, `XREAD BLOCK`…) are always refused: use the
  [terminal](/docs/services) and `redis-cli` for those.

## SQLite

Many apps keep a SQLite file in a [volume](/docs/files) (`/data/app.db`). Most
images have no `sqlite3`, so the manager brings its own SQLite client. It runs
in a helper container of the manager's own image that mounts the app's volumes,
with no network. On another server, its first use pulls that image.

- **Finding the files**: opening the tab scans the app's volumes for files that
  start with SQLite's header, whatever their name (`.db`, `.sqlite`, none).
  The scan goes 8 folders deep, looks at up to 50,000 entries and lists up to
  200 databases. The app doesn't need to be running.
- **Alongside the app**: SQLite's own locks keep the app and the browser apart,
  as with any second client. A read waits up to 5 s for an app's write to finish,
  and a save waits for the app's other writes. An app that holds an
  exclusive lock (`PRAGMA locking_mode = EXCLUSIVE`) can't be browsed while it
  runs: stop it first.
- **As the file's owner**: the helper opens the file as the user and group that
  own it. Any journal, `-wal` or `-shm` file it creates beside it is the app's,
  not root's, so the app can still open its own database.
- **Read-only** until you allow writes: the file is opened read-only, and SQLite
  refuses any write, `PRAGMA` included. With writes allowed, a save or console
  run takes SQLite's write lock (`BEGIN IMMEDIATE`) for one transaction.
- **One file at a time**: `ATTACH` is refused, with or without writes, since it
  could create or change any file of the volumes. So is `VACUUM` (it attaches a
  copy): run it from the app, or from its [terminal](/docs/services). The browser leaves `foreign_keys` off, SQLite's default: turn it
  on in the console if your app relies on it.
- **Only in a volume**: a database in the container's own filesystem isn't
  browsed. It's lost on the next deploy anyway: keep it in a volume.
- In the console, `DATE`, `DATETIME` and `TIMESTAMP` columns selected as they
  are show as `2026-01-02 15:04:05+00:00` whatever text they hold. Browsing shows
  the stored text.

## Access

Browsing, CSV export and the read-only console need the *data* permission;
editing and writing queries need *data write*. Every console run lands in the
[audit log](/docs/api) as `data console` or `data console (write)`, every save
as `data edit`, every create or drop as `data schema`, with the service but
without the query or values, which may hold secrets.

Endpoints, for scripts: `GET /api/services/{id}/data/tables`,
`GET /api/services/{id}/data/tables/{schema}/{table}` (`limit`, `offset`, `order`,
`desc`, `filters` as a JSON array of `{column, op, value}`), `…/export`,
`POST /api/services/{id}/data/tables/{schema}/{table}/changes`
(`{database, changes: {deletes, updates, inserts}, apply}`; without `apply`, it
only returns the statements),
`POST /api/services/{id}/data/collections/{collection}/changes` (the same for
documents), `POST /api/services/{id}/data/tables` (`{database, schema, name, columns, apply}`),
`POST …/tables/{schema}/{table}/drop` and `POST …/collections/{collection}/drop`
(`{database, apply}`), `POST /api/services/{id}/data/collections`
(`{database, name, apply}`), `POST /api/services/{id}/data/key/changes` (`{key, changes, apply}`; `create`
makes a new key),
`GET /api/services/{id}/data/keys` (`cursor`, `pattern`),
`GET /api/services/{id}/data/key` (`key`, `cursor`),
`GET /api/services/{id}/data/collections/{collection}/documents` (`database`,
`filter`, `sort`, `limit`, `skip`) and
`POST /api/services/{id}/data/console` (`{query, write, database}`). For an app,
`GET /api/services/{id}/data/databases` lists its SQLite files, `database` is
one of them (`<volume>/<path>`), and the schema is `main`.
