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

## Private registries

*Settings → Registries* stores credentials per registry host (Docker Hub, ghcr.io,
registry.gitlab.com, or any other). The manager's Docker checks them on save, as
`docker login` would; nothing is written to the hosts' Docker config. Every pull,
on any server, sends the credential of the image's registry. Use read-only tokens.
