# kipitiny — Design

Lightweight self-hosted PaaS (apps + Postgres with clean backups), similar in spirit to Dokploy, written in Go.

## 1. Goals

- Deploy and manage **Docker apps** and **PostgreSQL databases** on a server.
- **Clean, trustworthy backups** of Postgres are the core strength of the product.
- **Very low RAM usage.** Dokploy's Next.js app alone uses ~1.5 GB. Target: **30–100 MB total** management overhead (Go binary ~15–30 MB + Traefik ~40–80 MB).
- Ship as a **single static Go binary** (UI embedded), runnable as a container.

## 2. Non-goals (for now)

- No Docker Swarm, no Kubernetes.
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
| Backup storage | `github.com/minio/minio-go/v7` (any S3-compatible) + local disk |
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
- Strong authentication on the UI and API.
- Never expose the Docker API over TCP without TLS.
- Mask secrets (env vars, DB passwords, keys) in API read responses by default.

## 5. Core concepts

- **Project:** a group of services (apps + databases) sharing a private network.
- **Service:** an app (image or Git build) or a Postgres database.
- **Replica:** one running container of a service.
- **Deployment:** one release of a service, with an ID, status, and log file.

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
- The manager injects `DATABASE_URL=postgres://user:pass@<project>-db:5432/app` into app containers.
- Stricter isolation later: connect Traefik to each project network instead of one shared proxy network.

## 7. Reverse proxy (Traefik)

- Traefik is the only container listening on 80/443, deployed by the manager on first startup.
- Routing declared with labels on app containers; Traefik watches Docker and reconfigures instantly.
- Automatic HTTPS via Let's Encrypt.
- Separate container (not embedded) so manager restarts/updates never take apps offline.
- Optional Cloudflare Tunnel, one per server (token in Settings › Servers, or `KIPITINY_CLOUDFLARE_TUNNEL_TOKEN` for the manager's server): a managed `cloudflared` container on `kipitiny-proxy` forwards to Traefik's 443; Traefik then publishes no ports and runs no ACME (routers use its default cert). Hostnames are configured in the Cloudflare dashboard (wildcard recommended), or by the manager when Cloudflare is connected.
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

Databases get no Traefik labels (TCP/SNI routing is a possible later feature).

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
- **Files:** S3 or a shared named volume.
- **Migrations:** run once via a **pre-deploy command** in a one-off container.
- **Cron/background jobs:** separate service with 1 replica.
- **Postgres services are always 1 replica.**

## 9. Deployments (zero downtime, no Swarm)

1. Build or pull the new image.
2. Run the pre-deploy command once, if configured.
3. For each replica: start the new container with same Traefik labels, new name/deploy label.
4. Wait for health (`State.Health.Status == "healthy"`, or an HTTP check).
5. Traefik balances between old and new.
6. Stop and remove the old replica.
7. On failed health check: remove the new container, stop rollout, keep old replicas (automatic rollback).

Builds: Dockerfile-only via the Docker build API first. Nixpacks/Buildpacks later.

## 10. PostgreSQL management and backups (core feature)

### Provisioning

- One Postgres container per database service, on the private project network only.
- Generated password, named volume for data.
- Docker memory limit and sane defaults (`shared_buffers`, `max_connections`).

### Backup method

Run `pg_dump -Fc` **inside the database container** via `docker exec` so the dump tool matches the server version and the manager image needs no Postgres tools. Demux the exec stream with `stdcopy.StdCopy` into an `io.Pipe`, and stream it to S3 with `PutObject(size=-1)` (constant memory, no temp files). After upload, `ContainerExecInspect`: if exit code ≠ 0, delete the object and fail with stderr (pg_dump can fail after data started flowing). `-Fc` is already compressed and supports selective restore.

### What "clean backups" means

- **Check exit codes** and delete partial uploads.
- **Retention policies** (e.g. 7 daily, 4 weekly, 6 monthly), pruned automatically.
- **Metadata in SQLite:** size, duration, Postgres version, checksum, status.
- **Optional encryption** before upload (`age`).
- **Automated restore tests** (key differentiator): start a throwaway Postgres container, `pg_restore` the latest backup, run a sanity query, delete the container, record the result.
- **Project-level backups:** "back up everything in this project".
- **Restore flow:** `pg_restore` via exec with stdin attached; optionally stop the linked app during restore.

Later: point-in-time recovery via WAL archiving (`wal-g` or `pgBackRest`).

## 11. Manager state: SQLite

Single writer, low write volume, zero RAM overhead, state is one file.

- DSN pragmas: `journal_mode(WAL)`, `busy_timeout(5000)`, `foreign_keys(ON)`, `synchronous(NORMAL)`; `SetMaxOpenConns(1)`.
- DB file on a **volume** (`/data`), **local disk** only (never NFS/SMB).
- WAL mode has `.db`, `.db-wal`, `.db-shm`. Never delete WAL files while running.
- **Backing up SQLite:** `VACUUM INTO '/backups/kipitiny-<date>.db'`, then upload with the same S3 code. Optional: Litestream.
- `STRICT` tables.
- **Do not store** container logs or metrics in SQLite.

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
- Compress after completion; retention: optional "keep last N per service" in the cleanup job (Settings › Cleanup), which also prunes unused images, volumes, build cache, foreign stopped containers and networks.

Later: optional VictoriaLogs or Loki, opt-in.

## 13. UI

- React SPA (Vite), React Router, TanStack Query, Tailwind. No Next.js / Node at runtime.
- Embedded in the binary (`web/embed.go`), SPA fallback to `index.html`.
- Dev: Go on `:8080`, Vite dev server proxies `/api`.
- Release: multi-stage Dockerfile → distroless static image.

## 14. API and MCP

```
            ┌── REST API (web UI, CLI) ──┐
Clients ────┤                            ├──→ core (service layer) ──→ Docker / SQLite
            └── MCP tools (AI agents) ───┘
```

- All logic in `internal/core`. REST handlers and MCP tools are thin adapters. Permissions enforced in one place.
- MCP built in (`/mcp` module), Streamable HTTP at `/mcp`, reusing auth, tokens, permissions, audit logs. Standalone stdio proxy later.
- Task-oriented tools, not 1:1 REST mapping: `deploy_from_git`, `get_app_status`, `get_logs`, `rollback`, `backup_database`, `restore_database`.
- Mask secrets in every tool response; scoped tokens (read-only vs deploy); confirmation for destructive tools.

## 15. Multi-server (later)

- **No Swarm** (node-local volumes, no DB scaling, complicates exec-based backups).
- Remote Docker daemons over SSH/TLS (`client.WithHost("ssh://user@host")`).
- Each database lives on one known server; backup logic unchanged.
- Docker operations behind an interface taking a server parameter.
- Cross-server replicas: Traefik HTTP provider polling the manager, or proxy per server + DNS.

## 16. Roadmap

1. Manager in Docker with socket mounted; create/start/stop/delete containers; stream logs.
2. Traefik deployed on first start; deploy an app from an image with a domain + HTTPS.
3. Projects with private networks; Postgres per project; `DATABASE_URL` injection.
4. Manual Postgres backup → local disk → S3; restore.
5. Scheduled backups + retention + metadata.
6. Automated restore tests.
7. Blue-green deploys with health checks and rollback; pre-deploy command.
8. Replicas + reconciliation loop.
9. Builds from Git (Dockerfile first).
10. React UI polish; built-in MCP endpoint.
11. Multi-server over SSH.
12. Optional: Postgres backend for manager state, PITR, centralized logs.

## 17. Reference projects

- **Dokploy** (Next.js, Swarm, Traefik): feature reference; its Postgres + Redis + Next.js stack is what we avoid.
- **PG Back Web** (`eduardolat/pgbackweb`, Go): backup UX reference.
- **nuelScript/skiff** (Go): single-binary Docker PaaS with SQLite and label routing; very similar.
- **Kamal**: zero-downtime deploys without orchestration.
- **Dokku**: lightweight, mostly shell.
