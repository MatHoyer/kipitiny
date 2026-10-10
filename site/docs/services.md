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
  everyone (*Reset layout* puts them back). Click a node for its details,
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
- **Renaming** (Settings › Name) keeps everything else: volumes, backups,
  deploy history and the project network are tied to IDs, and nothing restarts.
  Containers are renamed in place. A renamed service also keeps answering to its
  old name on the project network until its next deploy, so update the apps
  that call it by hostname. Renaming a database rewrites the `{{ db.<name>.* }}`
  references to it and redeploys the apps using it. API tokens, scripts and MCP
  calls that name the project or service need the new name. Services of a
  project managed by git can't be renamed: the file owns their names, and
  renaming one there replaces it, volumes included.

## Networks

Two kinds of networks are automatic: each project has a private one (its
services reach each other by service name), and public apps share the proxy
network with Traefik. To let services of **different projects** talk, e.g.
several apps sharing one database:

1. *Infrastructure › Networks* › **Create network**, on the server the projects
   run on (only services on that server can join it).
2. In each service's *Settings › Networks*, tick the network and save (or
   draw a line from the service to the network on the **Map**). Running
   containers join or leave it right away; nothing restarts, and new replicas
   join it too.

On such a network a service answers as `<project>-<service>`, e.g. the `db`
database of project `shop` is `shop-db:5432`, so `{{ db.* }}` references
(which only cover the app's own project) don't apply: write the host in the
env value and the password as a secret. Renaming the project or service moves
that name at once. A database on such a network is still never public, but any
service on it can connect. An app in the host network can't join one.

These networks aren't part of a compose file: a project managed by git keeps
the ones set here across syncs. Deleting a network detaches its services first.
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

The Proton Pass CLI isn't part of the manager's image. The first connection
pulls `ghcr.io/mathoyer/kipitiny-protonpass` (about 60 MB) on the manager's
server and runs it as a container named `kipitiny-protonpass` while it's used
(a deploy, browsing in the env editor); it's removed after 5 minutes idle. Its
session lives in the `kipitiny-protonpass` volume, deleted on disconnect, and
is recreated from the saved token when lost (e.g. after restoring a manager
backup). Mirror the image and set `KIPITINY_PROTONPASS_IMAGE` if the server
can't reach ghcr.io.
