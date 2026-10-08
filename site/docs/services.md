---
title: Services
description: Apps, published ports, databases, environment and private registries
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
  (fields: `URL`, `HOST`, `PORT`, `USER`, `PASSWORD`, `DATABASE`).
- **Environment**: variables are readable, secrets write-only. A project holds shared
  ones that services reference as `{{ project.NAME }}`. References resolve at deploy.
- Deleting a database or a project destroys data and must be confirmed by typing its name.
- **Terminal** (service › Terminal): a shell in one of the service's running
  containers, `bash` when the image has it, else `sh`. *Settings › Servers*
  opens a root shell on a server itself. Admin only; every session is recorded
  in the audit log.
- **Map** (project › Map, or the whole install from the sidebar): what runs
  where, how traffic gets in (Traefik's entrypoints or a Cloudflare tunnel), the
  networks, and each container with the networks it is actually attached to.
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

## Private registries

*Settings → Registries* stores credentials per registry host (Docker Hub, ghcr.io,
registry.gitlab.com, or any other). The manager's Docker checks them on save, as
`docker login` would; nothing is written to the hosts' Docker config. Every pull,
on any server, sends the credential of the image's registry. Use read-only tokens.
