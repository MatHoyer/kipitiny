---
title: Services
description: Apps, published ports, databases, environment, networks and private registries
order: 3
---

- **Apps** run from an image, optionally public on a domain (HTTPS via Traefik), 1–10 replicas.
- **Published ports** bind host ports straight to an app's container, for
  traffic that isn't HTTP (a Minecraft server on `25565/tcp`, MQTT, a game on
  UDP…). Traefik isn't involved, so the client IP is real. Such an app runs one
  replica, and a host port can be published by one service per server (never
  Traefik's 80/443). Open them in the server's firewall yourself; a Cloudflare
  tunnel doesn't carry them. Such an app may have a domain without a container
  port, only for DNS (`mc.example.com:25565`); with Cloudflare connected, its
  record points at the server and is never proxied, even behind a tunnel.
- **PostgreSQL** services get generated credentials, a named data volume, a memory
  limit with matching `shared_buffers`, and a `pg_isready` healthcheck. They are
  only reachable inside their project, at `<service-name>:5432`. An app uses one or
  more databases through env references, e.g. `DATABASE_URL={{ db.main.URL }}`
  (fields: `URL`, `HOST`, `PORT`, `USER`, `PASSWORD`, `DATABASE`). The
  [data browser](/docs/data) shows its tables and runs queries.
- **PostGIS** is a PostgreSQL service whose image has the extension: pick
  *PostGIS* when adding a database (`postgis/postgis:17-3.6-alpine`), or set an
  existing PostgreSQL database's image to the PostGIS tag of the same major
  version (`postgres:17-alpine` → `postgis/postgis:17-3.6-alpine`). An image
  change between Alpine and Debian builds (`postgres:17` → `postgis/postgis:17-3.6-alpine`)
  is refused, like a major version change: they sort text differently, which
  corrupts indexes. Back up, create a new database with the image and restore
  into it. On a new
  data directory the image creates the `postgis`, `postgis_topology`,
  `fuzzystrmatch` and `postgis_tiger_geocoder` extensions in the main database;
  anywhere else (a database switched to PostGIS, another database of the
  instance) run `CREATE EXTENSION postgis;`. In a compose file, set
  `x-kipitiny.kind: postgres` on a `postgis/postgis` image. PostGIS publishes
  amd64 images only.
- **Redis, MySQL, MariaDB and MongoDB** services work the same way, each on its
  own port (`6379`, `3306`, `27017`). Their `URL` is `redis://:…@cache:6379`,
  `mysql://app:…@sql:3306/app` (MariaDB too) or
  `mongodb://app:…@docs:27017/app?authSource=admin`. A MySQL or MariaDB app
  user owns the database `app`; root's password stays with the manager, for
  backups. A MongoDB user is the instance's root, so an app may use other
  databases than `app`. The data browser covers PostgreSQL and Redis only.
- **Environment**: variables are readable, secrets write-only. A project holds shared
  ones that services reference as `{{ project.NAME }}`. References resolve at deploy.
  Values can also come from a password manager, see below.
- Deleting a database or a project destroys data and must be confirmed by typing its name.
- **Terminal** (service › Terminal): a shell in one of the service's running
  containers, `bash` when the image has it, else `sh`. *Settings › Servers*
  opens a root shell on a server itself. Admin only; every session is recorded
  in the audit log.
- **Map** (the home page for the whole install, and the first tab of each
  project): a canvas of
  what runs where, how traffic gets in (Traefik's entrypoints or a Cloudflare
  tunnel), which apps use which databases, and the networks created by hand.
  Pan, zoom and drag nodes (a project by its title); positions are saved for
  everyone (*Reset layout* puts them back). Nodes never sit on one another: a node
  dropped on others pushes them aside, and new ones take the nearest free
  spot. A service with several replicas shows a stack of cards and its count
  (×3); a project's previews stack behind its frame, and its *N previews*
  button spreads them out next to it (again to stack them back). **3D** tilts
  the map into an isometric view, to look at: pan, zoom and click work, dragging
  and drawing lines wait for the flat map. Click a node for its details,
  each container's addresses on the networks it is actually attached to, and
  Deploy, Restart or Stop. You can also edit from it:
  - drag a service's bottom dot onto a network to make it join (right away),
    or onto a database of its project to add a secret variable referencing it
    (`DATABASE_URL={{ db.<name>.URL }}`, applied on its next deploy);
  - select such a line and press Delete to undo it (the variables that
    reference the database are removed);
  - **Add** creates a project or a network, and the **+** on a project adds a
    service or database to it.
- Health, uptime checks and notifications: see [Monitoring](/docs/monitoring).
- **Renaming** (Settings › General) keeps everything else: volumes, backups,
  deploy history and the project network are tied to IDs, and nothing restarts.
  Containers are renamed in place. A renamed service also keeps answering to its
  old name on the project network until its next deploy, so update the apps
  that call it by hostname. Renaming a database rewrites the `{{ db.<name>.* }}`
  references to it and redeploys the apps using it; renaming an app rewrites
  the `{{ app.<name>.* }}` references to it (their value doesn't change). API tokens, scripts and MCP
  calls that name the project or service need the new name. Services of a
  project managed by git can't be renamed: the file owns their names, and
  renaming one there replaces it, volumes included.

## Tags

Tags group projects and services: a project's card on the projects page and a
service's card on its project's page show them, and both pages filter by them
(a filter keeps what has every selected tag, and stays in the address so it
can be shared).

- Set a project's tags in its **Settings**, a service's in **Settings ›
  General**. A tag is lowercased, up to 32 letters, digits, spaces and
  `. _ / : -`; up to 10 each.
- A project linked to git takes its services' tags from the compose file
  (`x-kipitiny.tags`, see [Compose](/docs/compose#x-kipitiny-per-service));
  its own tags are set in the UI as for any project. Changing tags never
  redeploys anything.
- Each tag gets a color from its name. Click a tag in the Tags card to pick
  another one: it applies wherever the tag appears. Admins may color any tag,
  members the tags of projects they may change the settings of.
- API: `PUT /api/projects/{id}/tags` and `PUT /api/services/{id}/tags` with
  `{"tags": [...]}`; `GET /api/tags` lists the tags in use with their colors,
  `PUT /api/tags/{name}` with `{"color": "blue"}` (empty: back to its own).
  MCP: `set_project_tags` and `set_service_tags`.

## Networks

Two kinds of networks are automatic: each project has a private one (its
services reach each other by service name), and public apps share the proxy
network with Traefik. To let services of **different projects** talk, e.g.
several apps sharing one database:

1. *Infrastructure › Networks* › **Create network**, on the server the projects
   run on (only services on that server can join it).
2. In each service's *Settings › Networking*, tick the network and save (or
   draw a line from the service to the network on the **Map**). Running
   containers join or leave it right away; nothing restarts, and new replicas
   join it too.

On such a network a service answers as `<project>-<service>`, e.g. the `db`
database of project `shop` is `shop-db:5432`, so `{{ db.* }}` references
(which only cover the app's own project) don't apply: write the host in the
env value and the password as a secret. Renaming the project or service moves
that name at once. A database on such a network is still never public, but any
service on it can connect. An app in the host network can't join one.

A compose file can set them too, as `external: true` networks listed by a
service (see [compose networks](/docs/compose#compose-keys-kipitiny-reads));
for services whose block has no `networks` key, a project managed by git keeps
the ones set here across syncs. A compose file can also split a project into
networks of its own, so that only some of its services reach each other. Deleting a network detaches its services first.
The **Map** draws them with a line to each of their services.

## Private registries

*Settings → Registries* stores credentials per registry host (Docker Hub, ghcr.io,
registry.gitlab.com, or any other). The manager's Docker checks them on save, as
`docker login` would; nothing is written to the hosts' Docker config. Every pull,
on any server, sends the credential of the image's registry. Use read-only tokens.

## Password managers

*Integrations › Password managers* connects **Proton Pass** with a personal
access token, scoped to the vaults you grant it. Env values (of an app or a
project entry) then reference its fields as `{{ pass://Vault/Item/field }}`, or
pick them in the env editor. They're read at each deploy and replica
recreation and never stored by kipitiny; a database's env can't use them.

Connect several accounts of the same password manager (personal and work)
by naming each one after the first: the first is the default connection,
referenced as `pass://`, and the one named `work` as
`{{ pass+work://Vault/Item/field }}`. The env editor asks which connection
a reference comes from. A reference to a named connection that doesn't exist
is refused when saving; one to a default connection that isn't connected
fails at deploy.

The Proton Pass CLI isn't part of the manager's image. The first connection
pulls `ghcr.io/mathoyer/kipitiny-protonpass` (about 60 MB) on the manager's
server and runs it as a container named `kipitiny-protonpass` while it's used
(a deploy, browsing in the env editor); it's removed after 5 minutes idle. Its
session lives in the `kipitiny-protonpass` volume, deleted on disconnect, and
is recreated from the saved token when lost (e.g. after restoring a manager
backup). A named connection has its own: `kipitiny-protonpass-work` for the
one named `work`. Mirror the image and set `KIPITINY_PROTONPASS_IMAGE` if the server
can't reach ghcr.io.
