# kipitiny — Design

Lightweight self-hosted PaaS (apps + Postgres with clean backups), similar in spirit to Dokploy, written in Go.

## 1. Goals

- Deploy and manage **Docker apps** and **PostgreSQL databases** on a server.
- **Clean, trustworthy backups** of Postgres are the core strength of the product.
- **Very low RAM usage.** Dokploy's Next.js app alone uses ~1.5 GB. Target: **30–100 MB total** management overhead (Go binary ~15–30 MB + Traefik ~40–80 MB).
- Ship as a **single static Go binary** (UI embedded), runnable as a container.

## 2. Non-goals (for now)

- No Docker Swarm services, no Kubernetes (Swarm's overlay network may be used for multi-server, §15).
- No multi-tenant hosted service.
- No database replication / HA Postgres.
- No manager high availability (single manager instance).

## 3. Tech stack

| Concern | Choice |
|---|---|
| Language | Go |
| Docker access | `github.com/moby/moby/client` |
| Manager state | SQLite via `modernc.org/sqlite` (pure Go, no CGO) |
| Query layer | Bun (`github.com/uptrace/bun`), SQLite and Postgres dialects |
| Migrations | goose (`github.com/pressly/goose/v3`), one folder per dialect |
| Scheduling | `github.com/robfig/cron/v3` + goroutines (no Redis, no queue) |
| Backup storage | `github.com/minio/minio-go/v7` (any S3-compatible) + local disk; Google Drive via `rclone`, Proton Drive via Proton's `proton-drive` CLI (both bundled) |
| Backup encryption (optional) | `filippo.io/age` |
| Reverse proxy | Traefik container, configured via Docker labels |
| UI | React SPA (Vite), embedded with `embed` |
| Optional logs backend | VictoriaLogs (opt-in, later) |

**Rule:** the manager is **one process**. No Postgres, no Redis, no worker containers for the manager itself.

## 4. Running the manager in Docker

The manager runs as a container and controls the **host** Docker daemon via the mounted socket (Docker-out-of-Docker). Managed containers are siblings, not nested. See `docker-compose.yml`.

- **Bind mount paths refer to the host filesystem**, not the manager container. Prefer named volumes.
- **Self-update:** a container cannot cleanly replace itself. Spawn a short-lived helper container that stops the old manager, pulls the new image, and starts it.
- Do **not** use Docker-in-Docker (`docker:dind`, privileged).

### Security

- Access to `docker.sock` = **root on the host**.
- Consider a socket proxy (`tecnativa/docker-socket-proxy`) limiting API endpoints.
- Strong authentication on the UI and API. Optional TOTP two-factor (own RFC 6238, codes single-use per time step, 10 hashed recovery codes) and passkeys (`go-webauthn`, discoverable + user verification, so a passkey sign-in skips TOTP). Passkeys bind to the host the UI is opened at. Pending second-factor tickets and WebAuthn ceremonies live in memory. Changing how the account signs in requires the password again; `kipitiny disable-2fa <user>` is the lockout escape hatch.
- Never expose the Docker API over TCP without TLS.
- Mask secrets (env vars, DB passwords, keys) in API read responses by default.

### Password managers

- `internal/secrets`: a `Provider` per password manager, each owning a reference scheme (`pass://` for Proton Pass; `op://` for 1Password would be another provider). Core and the UI only see the interface.
- Providers wrap the vendor's official CLI (Proton has no public API): bundled in the image, run as short-lived processes, no resident RAM. The manager starts itself (`kipitiny secrets-env`) under the CLI's `run` command to read resolved values back.
- Env values reference secrets as `{{ pass://Vault/Item/field }}`, in a service or a project entry. They're fetched on each deploy and replica recreation, injected into the containers and never stored by the manager; a database's env can't use them (backups read its credentials as stored).
- Providers that implement `secrets.Browser` feed the env editor's picker (vaults → items → fields): names and references only, admin-only routes.
- Logged in with a scoped token kept in settings; the CLI session lives in `$DATA_DIR/secrets/<provider>` and is recreated from the token when lost or expired.

## 5. Core concepts

- **Project:** a group of services (apps + databases) sharing a private network.
- **Service:** an app (a Docker image) or a database (Postgres or Redis).
  Its `icon` names the logo the UI shows (set by a template or the user);
  empty falls back to the database kind, then the image's base name
  (`ghcr.io/n8n-io/n8n` → `n8n`), then a generic box. Logos live in the UI
  (`web/src/components/service-icon.tsx`); backups keep the hint.
- **Replica:** one running container of a service.
- **Deployment:** one release of a service, with an ID, status, and log file.
- **Compose:** a project maps to a docker-compose file (§9.1), exported for
  any project, imported, or followed from git.

All managed resources carry labels:

```
kipitiny.managed=true
kipitiny.project=<project>
kipitiny.service=<service>
kipitiny.replica=<index>
kipitiny.deploy=<deploy-id>
```

## 6. Networking

1. **Shared proxy network** (`kipitiny-proxy`): Traefik + every public-facing app container.
2. **Private per-project network** (`kipitiny-<projectId>`): all containers of a project, including its Postgres.

```
                 ┌──────────── proxy network ────────────┐
Internet → Traefik ──→ app1-web        app2-web          │
                 └──────────┬───────────────┬────────────┘
                            │               │
                 app1 network          app2 network
                   app1-web              app2-web
                   app1-postgres         app2-redis
```

- Databases are **only** on the private network, never publicly reachable.
- Public containers are on both networks.
- Always set `traefik.docker.network=kipitiny-proxy` on containers attached to multiple networks, otherwise Traefik may pick the wrong network (502/504).
- Apps get credentials through env references resolved at deploy: `DATABASE_URL={{ db.<service>.URL }}` gives `postgres://user:pass@<service>:5432/app`, or `redis://:pass@<service>:6379` for Redis.
- Stricter isolation later: connect Traefik to each project network instead of one shared proxy network.

## 7. Reverse proxy (Traefik)

- Traefik is the only container listening on 80/443, deployed by the manager on first startup.
- Routing declared with labels on app containers; Traefik watches Docker and reconfigures instantly.
- Automatic HTTPS via Let's Encrypt.
- Separate container (not embedded) so manager restarts/updates never take apps offline.
- Optional Cloudflare Tunnel, one per server (token in Infrastructure › Servers, or `KIPITINY_CLOUDFLARE_TUNNEL_TOKEN` for the manager's server): a managed `cloudflared` container on `kipitiny-proxy` forwards to Traefik's 443; Traefik then publishes no ports and runs no ACME (routers use its default cert). Hostnames are configured in the Cloudflare dashboard (wildcard recommended), or by the manager when Cloudflare is connected.
- Optional Cloudflare API token (Settings): the manager syncs DNS for service domains in the token's zones (A to the server's public IP, or CNAME + tunnel route behind the tunnel), touching only records commented `managed by kipitiny`. Those domains get certificates via the Cloudflare DNS challenge (`letsencrypt-dns` resolver), which works behind the proxy.
- The manager's own UI (`KIPITINY_DOMAIN`) is routed by a file-provider config written into the Traefik container: the manager is created outside our control (compose), so it can't carry labels. It joins `kipitiny-proxy` itself (alias `kipitiny-manager`), or is reached via `host.docker.internal` when run on the host.

```go
Labels: map[string]string{
    "traefik.enable":                                          "true",
    "traefik.docker.network":                                  "kipitiny-proxy",
    "traefik.http.routers.shop-web.rule":                      "Host(`shop.example.com`)",
    "traefik.http.routers.shop-web.tls.certresolver":          "letsencrypt",
    "traefik.http.services.shop-web.loadbalancer.server.port": "3000",
}
```

Per-app middlewares (basic auth, IP allowlist, rate limit, response headers) are labels too. Traefik drops a router or middleware that containers define differently, and old and new replicas overlap during a rollout, so the router and its middlewares are named after a hash of their configuration (`kipitiny-<service>-<hash>`); a change adds a router next to the old one, both on the same load balancer. Basic auth references are hashed at deploy into the deployment snapshot, which reconciled replicas reuse. Behind Cloudflare (tunnel, or a record the manager keeps proxied) the client IP is the last `X-Forwarded-For` entry: Traefik trusts that header from the proxy network (tunnel) or Cloudflare's ranges (API token connected), and the middlewares read it with `ipStrategy.depth=1`.

Databases get no Traefik labels (TCP/SNI routing is a possible later feature).

### Published ports (non-HTTP apps)

Apps that speak something else than HTTP (game servers, MQTT, SMTP…) list **published ports** (`hostPort`, `containerPort`, `tcp`|`udp`), bound by Docker on every host address. Not Traefik TCP/UDP entrypoints: those are static configuration (each new port recreates Traefik, cutting every app), non-TLS TCP can only route `HostSNI(*)` (one service per entrypoint anyway) and the client IP is lost without PROXY protocol.

- Two containers can't bind one host port: an app with published ports runs **1 replica** and deploys stop-then-start (§9).
- A host port/protocol is used by one service per server, never Traefik's HTTP/HTTPS ports; checked on save, since Docker would only fail at deploy.
- The pre-deploy container publishes nothing.
- Databases can't publish ports: they stay private.
- The host firewall is the operator's; a Cloudflare tunnel only carries HTTP.
- The domain of such an app may have no container port: no Traefik route, the domain is only for DNS. Managed records of apps with published ports are always a DNS-only `A` to the server (Cloudflare's proxy and tunnels only carry HTTP), so the app's middlewares never trust `X-Forwarded-For`. Behind a tunnel Traefik publishes no port, so an app there can't route HTTP on that domain too (refused on save).

## 8. Replicas

Replicas on one host = several identical containers with **identical Traefik router/service labels** and different names (`shop-web-1`, `shop-web-2`). Traefik merges them into one load balancer.

### Reconciliation loop

- **Desired state** (replica counts, config) lives in SQLite.
- **Actual state** computed from Docker (list containers by label).
- Reconcile every 10–30 s, after every change, and on Docker events.
- Do not write to SQLite from the loop unless something changed.
- Remove non-running containers, start missing indices `1..Replicas`, stop+remove indices above `Replicas`.

### Requirements for user apps

- **Stateless:** sessions in DB/Redis/signed cookies.
- **Files:** S3 or an app **volume**: a named volume (`kipitiny-vol-<service>-<name>`, labelled with project and service) mounted at a path in every replica and the pre-deploy container. Replicas share it, so concurrent writers must cope. Docker creates it on first use; it survives deploys and goes away with the service (delete confirms with the name). A volume removed from the settings keeps its data until then.
- **Migrations:** run once via a **pre-deploy command** in a one-off container.
- **Cron/background jobs:** separate service with 1 replica.
- **Postgres services are always 1 replica.**

## 9. Deployments (zero downtime, no Swarm)

1. Pull the new image (built and pushed by CI; kipitiny does not build).
2. Run the pre-deploy command once, if configured.
3. For each replica: start the new container with same Traefik labels, new name/deploy label.
4. Wait for health (`State.Health.Status == "healthy"`, or an HTTP check).
5. Traefik balances between old and new.
6. Stop and remove the old replica.
7. On failed health check: remove the new container, stop rollout, keep old replicas (automatic rollback).

Apps with published ports can't overlap, so they **swap**: pre-deploy, stop the old replica (kept for its logs), start the new one, wait for health (probe on the HTTP port, else the first published TCP port, else a stability wait). On failure the new container is removed and the old one started again. Short downtime by design.

Apps have a **stop grace period** (seconds after SIGTERM before SIGKILL, default 10, max 600), used by every stop the manager makes and set as the container's `StopTimeout` so Docker's own stops honour it. Databases keep their fixed timeouts (Postgres 60 s, Redis 30 s).

No builds on the server (Git builds existed and were removed to keep services simple; may return later; git now only describes services, §9.1). Flow: build once in CI, then deploy by tag (`kipitiny deploy`, a deploy-scoped token that may change the tag, never the repository). Every pull is pinned to its digest in the deployment record, so reconciler recreations and rollbacks never pick up a moved tag.

### 9.1 Compose files and GitOps

A project is also a docker-compose file, so it is never locked in and can be described in git.

- `internal/compose` (pure: no Docker, no store) parses and writes the subset kipitiny runs: `image`, `environment`, `ports`, named `volumes`, `deploy.replicas`, `deploy.resources.limits`, `stop_grace_period`, plus a per-service `x-kipitiny` block (kind, domain, port, health path, pre-deploy/backup, icon, secrets, middlewares, database password) and a top-level one (project variables). Docker Compose ignores `x-*`. Harmless unknown keys are dropped with a warning; keys that would change what runs are errors. Compose interpolation (`${NAME}`, `$$`) applies outside `x-kipitiny`, whose values core resolves only when a whole value is `${NAME}` (bcrypt hashes are full of `$`).
- **Export:** secret values (service and project secrets, database passwords, basic auth hashes) become `${SERVICE_KEY}` variables; references stay verbatim. Exporting *with secrets* returns the `.env` too: session only, password re-entered, audited.
- **Apply** (`core.ApplyCompose`): parse, validate every service against the project's variables and databases *as they will be*, and only then create/update (through the normal service functions), optionally prune apps (never databases: reported as orphaned), and deploy created and changed services, databases first (apps wait for them, so migrations find their database). A missing `${NAME}` that is a whole env value becomes `{{ project.NAME }}`, unless it is an existing secret's export placeholder, which keeps its value. One apply per project at a time.
- **Reference:** `docs/compose.md` is the format's documentation, embedded in the binary and served at `/llms.txt` (public: no secrets), in the UI and by the MCP `get_compose_reference` tool. A test fails when a key the parser reads or drops isn't documented, or an example doesn't parse.
- **Git:** `project_git` links a project to a branch and path (HTTPS, optional token). A loop polls with `ls-remote` (no fetch) and, on a new commit, shallow-clones into a temp dir under `$DATA_DIR/git` (disk, not RAM), reads the file and applies it with pruning; push webhooks and "sync now" force it. Linking shows a dry run first. While linked, service create/update/delete outside the sync is refused (`ErrGitManaged`), except deleting an orphaned database. Each service stores the hash of its compose block: a sync skips a service whose block is unchanged, so a tag deployed by CI (deploy token) stays until the file changes that service. The images the last sync set are kept to show such drift. Outcomes go to `git.sync.failed` / `git.sync.succeeded`.

## 10. PostgreSQL management and backups (core feature)

### Provisioning

- One Postgres container per database service, on the private project network only.
- Generated password, named volume for data.
- Docker memory limit and sane defaults (`shared_buffers`, `max_connections`).

### Redis

- Same model as Postgres: one container, one replica, private network only, named volume, password generated at creation and fixed.
- AOF persistence (`appendonly yes`); `maxmemory` at 75% of the container limit so Redis refuses writes instead of being OOM-killed.
- Backed up as a volume (see below): a crash-consistent copy of the AOF, which Redis loads even if its tail is cut.

### Backup method

Run `pg_dump -Fc` **inside the database container** via `docker exec` so the dump tool matches the server version and the manager image needs no Postgres tools. Demux the exec stream with `stdcopy.StdCopy` into an `io.Pipe`, and stream it to S3 with `PutObject(size=-1)` (constant memory, no temp files). After upload, `ContainerExecInspect`: if exit code ≠ 0, delete the object and fail with stderr (pg_dump can fail after data started flowing). `-Fc` is already compressed and supports selective restore.

### What "clean backups" means

- **Check exit codes** and delete partial uploads.
- **Retention policies** (e.g. 7 daily, 4 weekly, 6 monthly), pruned automatically.
- **Metadata in SQLite:** size, duration, Postgres version, checksum, status.
- **Optional encryption** before upload (`age`).
- **Automated restore tests** (key differentiator): start a throwaway Postgres container, `pg_restore` the latest backup, run a sanity query, delete the container, record the result.
- **Project-level backups:** "back up everything in this project".
- **Restore flow:** `pg_restore` via exec with stdin attached; apps referencing the database are stopped during restore.

### Volume backups

Every other stateful service is backed up by archiving its volumes: Redis's data volume, an app's named volumes. Same targets, encryption, schedules, retention, metadata (kind `volume`, volume names) and restore tests as Postgres.

- A short-lived `busybox` helper (no network, labelled `kipitiny.component=volume-helper`) mounts the volumes read-only under `/v/<name>`; `tar -czf - --numeric-owner` streams through `docker exec` into the same upload path as `pg_dump`. The service keeps running; an app's optional *pre-backup command* runs in one replica first (e.g. flush to disk).
- Restore: extract into a fresh staging volume through a helper, verify the checksum and that every volume is present, and only then stop the service's containers, replace each volume's content (`rm` + `cp -a` from staging) and start them again. A failure before the swap leaves live data untouched.
- Restore test: stream the archive through `tar -tzf -` in a throwaway helper, check the checksum and record the entry count.
- Helpers and staging volumes left by a crash are removed at startup only (a server re-check must not kill a running restore).

Later: point-in-time recovery via WAL archiving (`wal-g` or `pgBackRest`).

## 11. Manager state: SQLite

Single writer, low write volume, zero RAM overhead, state is one file.

- DSN pragmas: `journal_mode(WAL)`, `busy_timeout(5000)`, `foreign_keys(ON)`, `synchronous(NORMAL)`; `SetMaxOpenConns(1)`.
- DB file on a **volume** (`/data`), **local disk** only (never NFS/SMB).
- WAL mode has `.db`, `.db-wal`, `.db-shm`. Never delete WAL files while running.
- **Backing up SQLite:** `VACUUM INTO '/backups/kipitiny-<date>.db'`, then upload with the same S3 code. Optional: Litestream.
- `STRICT` tables.
- **Do not store** container logs or metrics in SQLite. Exception: uptime checks keep one row per service and hour (counts and summed response time), pruned after 30 days.

### Postgres compatibility

Now: all data access behind `store.Store`; Bun; portable SQL (TEXT ULIDs generated in Go, consistent timestamps, Go bools, `ON CONFLICT`/`RETURNING` OK, JSON as text; avoid arrays, `LISTEN/NOTIFY`, advisory locks, enums, SQLite-only tricks).

Later: `pgstore` + `migrations/postgres/`, CI against both, `kipitiny migrate-db --to postgres://...`. The real reason for Postgres would be manager HA, not performance.

## 12. Logs

### Container logs: kept by Docker

- Stream from the Docker API on demand (SSE/WebSocket), never load fully into memory, cancel on client disconnect.
- **Always set log rotation:** `LogConfig{Type: "local", Config: {"max-size": "10m", "max-file": "3"}}`.
- Replicas: one stream per container, lines prefixed (`web-1 |`).

### Redeploys delete logs

1. Keep stopped old containers for a while (e.g. 1 h or until next deploy). **Start here.**
2. Before removal, save last N lines to `/data/logs/<project>/<service>/<ts>-deploy-<id>.log.gz`. **Later.**
3. Persist continuously to rotating files.

### Deploy/build logs

- One file per deployment: `/data/deploys/<project>/<service>/<deploy-id>.log`; only path + metadata in SQLite.
- Compress after completion; retention: optional "keep last N per service" in the cleanup job (System › Cleanup), which also prunes unused images, volumes, build cache, foreign stopped containers and networks.

Later: optional VictoriaLogs or Loki, opt-in.

### Notifications

- `internal/notify` is transport-agnostic: an `Event` (type, level, title, message, fields, link) and a `Sender` per channel kind. Each kind declares its config fields (secret or not), so the UI renders new kinds without changes.
- Kinds: Discord webhook now; email (SMTP), in-app and generic webhooks later.
- Channels live in `notification_channels` and subscribe to event types (deploy/backup/restore/restore-test outcomes, restarted services, cleanup errors, new versions). Secrets are masked on read.
- Core emits events where operations finish; delivery runs in the background with a timeout, and keyed events (a crash loop) are throttled.
- Health alerts: each reconcile round judges every deployed, running service from the containers it already listed. It is healthy while a replica of the current deployment runs and passes its healthcheck (or has none). After 2 minutes without one, `service.unhealthy` goes out, then `service.healthy` once it recovers. Docker doesn't restart unhealthy containers, so this is the only signal for an app or database that runs but no longer answers, public or not. Services being deployed, restarted or restored are skipped. The state is in memory: after a manager restart an ongoing problem is reported again.

### Resource usage

- Every 10 s the manager takes a one-shot `ContainerStats` of each running replica that serves traffic, on every server, and sums them per service: CPU (percent of one core, from the CPU time delta between two samples), memory (without reclaimable page cache, like `docker stats`), network bytes/s.
- Kept in memory only: the last five minutes per service. Nothing in SQLite, no metrics container.
- `GET /api/services/{id}/stats` (current + history, for the service page sparklines), `GET /api/stats` (current use of every service, which project and server lists add up).

### Uptime checks

- One optional check per app with a public domain: `GET https://<domain><path>` every 30–3600 s (default 60), with a timeout and an expected status (default: any below 400; redirects are not followed). Runs in the manager through `internal/probe`, no extra container. Stopped or undeployed services are skipped.
- Debounce: down after 3 failures in a row, up again after 2 successes. Transitions go out as `uptime.down` / `uptime.up` events through the notification channels; the notified state is stored on the check so a restart doesn't repeat or lose it.
- The last 60 results live in memory (the service page's bars and response times); hourly counts in `uptime_hours` give the 24 h / 7 d / 30 d percentages.

## 13. UI

- React SPA (Vite), React Router, TanStack Query, Tailwind. No Next.js / Node at runtime.
- Embedded in the binary (`web/embed.go`), SPA fallback to `index.html`.
- Dev: Go on `:8080`, Vite dev server proxies `/api`.
- Release: multi-stage Dockerfile → distroless image (`cc` variant, for the bundled `pass-cli`; kipitiny itself is static).
- Web terminal: `docker exec` with a TTY, bridged to xterm.js (lazy-loaded chunk) over a websocket (`golang.org/x/net/websocket`). Browser sends JSON `input`/`resize` messages; the server sends raw output as binary frames, then one `exit`/`error` message. Admin only, Origin must match the host, every session opened/closed lands in the audit log.
- Server terminal: same bridge, into a throwaway privileged `alpine` container (`--pid=host`, labelled `kipitiny.component=terminal`) running `nsenter -t 1 -m -u -i -n -p` then `su -l root`: a root shell on the host, local or remote, with nothing but Docker access (already root-equivalent). Closing the session removes the container; leftovers are cleaned at startup.

## 14. API and MCP

```
            ┌── REST API (web UI, CLI) ──┐
Clients ────┤                            ├──→ core (service layer) ──→ Docker / SQLite
            └── MCP tools (AI agents) ───┘
```

- All logic in `internal/core`. REST handlers and MCP tools are thin adapters. Permissions enforced in one place.
- MCP built in (`/mcp` module), Streamable HTTP at `/mcp`, reusing auth, tokens, permissions, audit logs. Standalone stdio proxy later.
- Task-oriented tools, not 1:1 REST mapping: `deploy_image`, `get_app_status`, `get_logs`, `rollback`, `backup_database`, `restore_database`.
- Mask secrets in every tool response; scoped tokens (read-only vs deploy); confirmation for destructive tools.

## 15. Multi-server

Shipped: remote Docker daemons over SSH (manager key, host key pinned on first use). A project lives on one server, fixed at creation; each server runs its own Traefik and optional tunnel, and DNS points a domain at its project's server. Each database lives on one known server; backup logic unchanged.

**No Swarm services.** Swarm volumes are node-local, so databases would be pinned with constraints anyway; backups would still have to find the task's node and exec through its daemon; and the reconciler, rollouts, logs, terminal and probes would be rewritten for the service/task API. Single-server installs (most of them) would carry raft, ingress and VXLAN for nothing.

### Replicas across servers (later, issue #79)

Deferred until there is a concrete need. Planned approach:

1. **Placements:** `service_placements (service_id, server_id, replicas)`; no rows = all replicas on the project's server. The reconciler iterates placements; containers carry `kipitiny.server`; the project network is created on every placed server. Databases stay on the project's server, 1 replica.
2. **Routing:** one Traefik per server (unchanged, it sees its local replicas) + one A record per placed server in the DNS sync. Spread services require a Cloudflare zone (DNS-01: the TLS challenge breaks with several A records) and refuse servers behind a tunnel.
3. **Rollouts** go server by server with the health gate; a failure rolls already-updated servers back to the previous digest.
4. **Cross-server networking: Swarm overlay only.** `swarm init` on the manager's server when a second server is added, `swarm join` on the others, and one attachable encrypted overlay (`docker network create -d overlay --attachable --opt encrypted`) per spread project. Plain containers join it, so `{{ db.X.host }}` keeps resolving by name and nothing else changes. Needs 2377/tcp, 7946/tcp+udp, 4789/udp between servers; IPsec ESP must not be blocked. Fallback if that is impractical: a manager-run WireGuard mesh with databases published on the mesh IP only.
5. **Failover** (optional): replicas of an unreachable server restarted on another placed server after a delay, by the reconciler.

## 16. Roadmap

1. Manager in Docker with socket mounted; create/start/stop/delete containers; stream logs.
2. Traefik deployed on first start; deploy an app from an image with a domain + HTTPS.
3. Projects with private networks; Postgres per project; `DATABASE_URL` injection.
4. Manual Postgres backup → local disk → S3; restore.
5. Scheduled backups + retention + metadata.
6. Automated restore tests.
7. Blue-green deploys with health checks and rollback; pre-deploy command.
8. Replicas + reconciliation loop.
9. ~~Builds from Git~~ (removed: images built in CI, deployed by tag).
10. React UI polish; built-in MCP endpoint.
11. Multi-server over SSH.
12. Optional: Postgres backend for manager state, PITR, centralized logs.

## 17. Reference projects

- **Dokploy** (Next.js, Swarm, Traefik): feature reference; its Postgres + Redis + Next.js stack is what we avoid.
- **PG Back Web** (`eduardolat/pgbackweb`, Go): backup UX reference.
- **nuelScript/skiff** (Go): single-binary Docker PaaS with SQLite and label routing; very similar.
- **Kamal**: zero-downtime deploys without orchestration.
- **Dokku**: lightweight, mostly shell.
