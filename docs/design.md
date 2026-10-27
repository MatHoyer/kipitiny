# kipitiny — Design

Lightweight self-hosted PaaS (apps + Postgres with clean backups), written in Go.

## 1. Goals

- Deploy and manage **Docker apps** and **PostgreSQL databases** on a server.
- **Clean, trustworthy backups** of Postgres are the core strength of the product.
- **Very low RAM usage.** Target: **30–100 MB total** management overhead (Go binary ~15–30 MB + Traefik ~40–80 MB).
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
- Strong authentication on the UI and API. Optional TOTP two-factor (own RFC 6238, codes single-use per time step, 10 hashed recovery codes) and passkeys (`go-webauthn`, discoverable + user verification, so a passkey sign-in skips TOTP). Passkeys bind to the host the UI is opened at. Pending second-factor tickets and WebAuthn ceremonies live in memory. Changing how the account signs in requires the password again; `kipitiny disable-2fa <user>` is the lockout escape hatch.
- Never expose the Docker API over TCP without TLS.
- Users: **admins** do everything; **members** get per-project permissions (`project_grants`: view, deploy, settings, env, domains, backups, restore, download, data, data-write, files, files-write, terminal, git, delete) and read-only server **features** (`users.features`). Defined in `internal/core/permissions.go`. A project a member can't see is reported as not found. Host network and the Docker socket stay admin-only (root on the host).
- Enforcement: every REST route maps to what it needs in `internal/api/access.go` (checked after the mux sets `{id}`; `TestRouteAccess` fails on an unmapped route); core checks what depends on the body (env, domains and password-manager refs in a service change, compose prunes, restores across projects); MCP tools check the same permissions. Tokens belong to a user and never exceed them (no scopes: permissions are the only limit); a restricted token (`api_tokens.restricted`, with its own features and grants) acts as a member holding what both it and its owner hold, checked again on every request. Git syncs run as the user who linked the repo. Lists are filtered, and server-level views drop hosts, accounts and URLs for members.
- Single sign-on: OpenID Connect providers (`sso_providers`, `internal/core/sso.go`, `coreos/go-oidc`) added by admins; the issuer's discovery document is read on save and cached per provider. Providers never create accounts: `sso_identities` links a provider's subject to a user, from Account (password confirmed first). Sign-in is the authorization code flow with PKCE and a nonce; the flow lives in memory under its state, which a cookie scoped to the callback path also holds, so a code can't be replayed into another browser. The provider replaces the password only: TOTP still applies. The callback answers with redirects carrying the outcome (`?sso-error=`, the second-factor ticket in `#sso-mfa=`). An invite can be accepted through a provider: the account is created without a password (`password_hash` empty, never matching), and `confirmedUser` then takes a confirmation of the session in place of the password: a sign-in at the provider with `prompt=login&max_age=0` whose `auth_time` is no older than the flow (a provider reusing its own session doesn't count), or a passkey sign-in; it lasts 10 minutes, in memory. The last passkey or identity of such an account can't be removed. *Require single sign-on* (setting `sso_required`) refuses password sign-in (and second-factor tickets that came from a password), passkey sign-in and password invites; turning it on needs the admin's own linked identity, the last provider can't be removed meanwhile, and `kipitiny allow-password-login` turns it off.
- Accounts come from single-use invite links (SHA-256 of the token stored, 7 days), optionally emailed through the SMTP server (the browser sends the invite page's URL; the token goes in its fragment). The last admin can't be demoted or removed; `kipitiny make-admin <user>` is the escape hatch.
- Mask secrets (env vars, DB passwords, keys) in API read responses by default.

### Password managers

- `internal/secrets`: a `Provider` per password manager, each owning a reference scheme (`pass://` for Proton Pass, `op://` for 1Password would be another provider). Core and the UI only see the interface.
- Providers wrap the vendor's official CLI (Proton has no public API), kept out of the manager's image: Proton Pass runs `ghcr.io/mathoyer/kipitiny-protonpass` (`images/protonpass`, tagged by pass-cli version, `KIPITINY_PROTONPASS_IMAGE`) on the manager's own Docker, pulled on first use. One helper container (`sleep`, labelled `kipitiny.component=secrets`, read-only root, no capabilities) starts on the first call; each call is a `docker exec` into it, and it's removed after 5 minutes idle, at startup and at shutdown. Optional integrations may run as on-demand containers; the manager's own state never does.
- Resolution: references go in the exec's env as `KIPITINY_SECRET_<i>`, `pass-cli run -- env -0` prints them resolved, NUL-separated. The token reaches `pass-cli login` on stdin, never in a container or exec config.
- Env values reference secrets as `{{ scheme://Vault/Item/field }}`, in a service or a project entry. They're fetched on each deploy and replica recreation, injected into the containers and never stored by the manager; a database's env can't use them (backups read its credentials as stored).
- Providers that implement `secrets.Browser` feed the env editor's picker (vaults → items → fields): names and references only, admin-only routes, cached in memory for 5 minutes (`?refresh` bypasses it; dropped on connect/disconnect).
- Logged in with a scoped token kept in settings; the CLI session lives in the `kipitiny-protonpass` volume (not in manager backups), removed on disconnect, and is recreated from the token when lost or expired.

## 5. Core concepts

- **Project:** a group of services (apps + databases) sharing a private network.
- **Service:** an app (a Docker image) or a database (Postgres, Redis, MySQL, MariaDB or MongoDB).
  Its `icon` names the logo the UI shows (set by a template or the user);
  empty falls back to the database kind, then the image's base name
  (`ghcr.io/n8n-io/n8n` → `n8n`), then a generic box. Logos live in the UI
  (`web/src/components/service-icon.tsx`); backups keep the hint.
- **Replica:** one running container of a service.
- **Deployment:** one release of a service, with an ID, status, and log file.
- **Host access:** an app may run in the host's network (`network_mode: host`: no project network, domain or published ports; one replica, stop-then-start deploys) and mount the host's Docker socket (ro/rw), for agents such as Beszel's. Nothing else on the host can be bind-mounted.
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
- Apps get credentials through env references resolved at deploy: `DATABASE_URL={{ db.<service>.URL }}` gives `postgres://user:pass@<service>:5432/postgres` (new services use the instance's default `postgres` database; older ones keep `app`), or `redis://:pass@<service>:6379` for Redis. `{{ app.<service>.URL }}` / `.DOMAIN` give where an app of the same project is public (checked at deploy, not on save, since a compose file may create the app), also inside project variables, so a copy of a project (previews) needs no URL overrides.
- **Networks created by hand** (`kipitiny-net-<id>`, Infrastructure › Networks): one server each; services of any project there join them (`services.networks`, set on its own, or by a compose service listing `networks`: `external` ones by name; a service without the key keeps them across syncs) under the alias `<project>-<service>`. Applied to running containers with connect/disconnect, kept in line by the reconciler; aliases move on rename.
- **Compose networks**: a file's own (non-external) networks are per project, `kipitiny-<project>-<name>` (`docker.ProjectNetworkNamed`; `default` is the project network), listed in `services.project_networks` (empty = default only). Containers join exactly those with their service name as alias, so services share only the networks they're both on; a change redeploys. Removed with the project.
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

**Maintenance pages** (no extra container): the manager serves them under `/api/pages/`, reached by each Traefik through the proxy network (manager's server) or `https://KIPITINY_DOMAIN` (remote servers; none without it). Three pieces:
- File provider (static, in Traefik's command hash): service `kipitiny-pages` and errors middleware `kipitiny-down` (502/504 → 503, `{url}` names the app, only `Accept` and `Accept-Language` forwarded), set on the `websecure` entrypoint. Static on purpose: a missing middleware would break every router while the manager is down. Apps' own 503s pass through.
- HTTP provider (`/api/pages/traefik/<server>`, polled every 5 s, HMAC token per server): per routed app a priority-1 `Host()` router to its page, which catches the app when no replica carries its router (Traefik drops routers of stopped or unhealthy containers); while its project is in maintenance mode a router above the app's. With allowed IPs that router goes to `kipitiny-<id>@docker` through an `ipAllowList` rejecting with 418, turned into the page by an errors middleware, then the app's own chain read from a running replica's labels.
- Pages answer 503 + `Retry-After` under a sandbox CSP (custom HTML gets scripts but an opaque origin, also on the manager's domain). Settings live in `projects.maintenance` (one page and mode for every app of the project; previews copy the parent's pages on each deploy, keeping their own mode), set on their own (never by compose), so git projects have them too. `/api/pages/<id>` takes an app's ID (Traefik) or the project's (to see it from the settings).

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

### Sleep mode

Projects may set `sleep_after_seconds` (5 min–30 days; set on its own, not in compose files). It needs an app with a domain and port, and no service with published ports or the host network (their traffic can't wake it; checked on save, on service changes while it's on, and by the loop). Idle signal: Traefik's Prometheus router counters (`traefik_router_requests_total`, routers labelled), on an entrypoint bound to `127.0.0.1:8082` inside the Traefik container and read every minute by an exec (`wget`), so remote servers work the same and apps on the proxy network can't read them. Counters are summed per project over its services' `kipitiny-<id>-<hash>@docker` routers (the pages' `@http` routers don't count); any change is activity, kept in memory only (a restart counts every project as just active). An idle project (no service busy: deploys and backups hold their lock) gets `sleeping=1` on its running services, then they stop, apps first (kept). Databases and workers sleep too: background jobs don't run. The reconciler, health alerts and uptime checks skip sleeping services. A request hits the priority-1 page router (§7), whose page wakes the project in the background (`wakeProject`: clear the flags, services in `startOrder`, databases first then apps after their `depends_on`, each awaited as `awaited` says: databases and `service_healthy` ready, jobs exited 0, 2 min max; jobs that exited 0 stay so) and answers a "Starting…" page that reloads itself every 3 s (`Refresh` header, so a custom page from `services.maintenance.starting` reloads too; pages stay sandboxed) until the app's router answers instead, or for API clients (no `text/html` in `Accept`) a JSON 503 with `Retry-After: 5`; after 2 min it falls back to the usual page. Whatever needs a container wakes the project first (`wakeFor`): deploys and rollbacks (pre-deploy needs the databases), starts, database backups, restores and queries, terminals. Remote servers need `KIPITINY_DOMAIN` for it (refused on save otherwise); a stop wins over sleep.

### Requirements for user apps

- **Stateless:** sessions in DB/Redis/signed cookies.
- **Files:** S3 or an app **volume**: a named volume (`kipitiny-vol-<service>-<name>`, labelled with project and service) mounted at a path in every replica and the pre-deploy container. Replicas share it, so concurrent writers must cope. Docker creates it on first use; it survives deploys and goes away with the service (delete confirms with the name). A volume removed from the settings keeps its data until then.
- **Migrations:** run once via a **pre-deploy command** in a one-off container.
- **Background workers:** separate service with 1 replica.
- **Cron:** scheduled jobs (`jobs` table, per app): the backup scheduler runs the command (`sh -c`, entrypoint cleared) in a one-off container built like the pre-deploy one from the current deployment's snapshot (pinned image, env, networks, volumes; no proxy network, no ports, `restart: no`), labelled `kipitiny.component=job` + `kipitiny.job`. One run at a time per job (in memory), timeout kills it, last 2000 log lines kept in `data/jobs/<service>/<run>.log`, last 50 runs per job. A run wakes a sleeping project and keeps it awake; a stopped app's jobs don't run. Shutdown cancels runs (`interrupted`); the reconciler removes job containers no run owns. `job.failed` (cooldown per job) / `job.succeeded`.
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

No builds on the server (Git builds existed and were removed to keep services simple; may return later; git now only describes services, §9.1). Flow: build once in CI, then deploy by tag (`kipitiny deploy`, a token with *deploy* may change the tag, never the repository). Every pull is pinned to its digest in the deployment record, so reconciler recreations and rollbacks never pick up a moved tag.

### 9.1 Compose files and GitOps

A project is also a docker-compose file, so it is never locked in and can be described in git.

- `internal/compose` (pure: no Docker, no store) parses and writes the subset kipitiny runs: `image`, `environment`, `ports` (with host IP and ranges), named `volumes` (`:ro`; anonymous ones get a name from their path; top-level `external` and local `driver_opts`, admin-only like host access: `store.Volume.HostAccess`), `deploy.replicas`, `deploy.resources.limits`, `stop_grace_period`, the container settings in `services.runtime` (JSON `store.Runtime`: `command`, `entrypoint`, `user`, `working_dir`, `labels` but `traefik.*`/`kipitiny.*`, `tmpfs`, `shm_size`, `ulimits`, `sysctls`, `read_only`, `init`, `stop_signal`, `tty`, `stdin_open`, `extra_hosts`, and `cap_add`, `cap_drop`, `devices`, `privileged`, `security_opt` and absolute bind mounts (`binds`), which only admins set, like host access, and previews leave out; apps only), files in `services.files` (`store.ServiceFile`: top-level `configs`/`secrets` and repository paths bind-mounted, written into containers before they start by `docker.Run`; a secret's references resolve at deploy; API views leave contents out; repository paths come from the clone through `compose.Vars.Files`, read while `withGitFile` holds it), `env_file` (git only), a database's user/database name (`databaseNameKeys`, taken at creation like the password, exported) and `/docker-entrypoint-initdb.d` files (changes don't redeploy), `deploy.restart_policy` (→ `Runtime.Restart`), `deploy.resources.reservations.memory` (→ `Runtime.MemoryReservationMB`), `profiles` (`[preview]` = `x-kipitiny.preview: only`: skipped by the parent's apply, neither created nor pruned; `skip` is dropped by `previewFile`; other profiles drop the service; without git, kept as `services.preview`, which previews of such projects copy through `exportFile`: an `only` service is created but refused by `startDeploy`/`ServiceAction` and left out by `needsDeploy`, and switching to it stops the containers for good), `extends` (merged at the YAML node level before parsing, as Compose merges; another file's relative paths rebased) and top-level `include` (each file parsed on its own, paths relative to it, services merged; a name in two files is an error) — other files through `Vars.Files`, so git only, nested at most 5 levels, plus a per-service `x-kipitiny` block (kind, domain, port, health path, pre-deploy/backup, icon, secrets, middlewares, database password, uptime check) and a top-level one (project variables). Docker Compose ignores `x-*`. Harmless unknown keys are dropped with a warning; keys that would change what runs are errors. Compose interpolation (`${NAME}`, `$$`) applies outside `x-kipitiny`, whose values core resolves only when a whole value is `${NAME}` (bcrypt hashes are full of `$`).
- **Export:** secret values (service and project secrets, database passwords, basic auth hashes) become `${SERVICE_KEY}` variables; references stay verbatim. Exporting *with secrets* returns the `.env` too: session only, password re-entered, audited.
- **Apply** (`core.ApplyCompose`): parse, validate every service against the project's variables and databases *as they will be*, and only then create/update (through the normal service functions), optionally prune apps (never databases: reported as orphaned), and deploy created and changed services, databases first (apps wait for them, so migrations find their database), then apps in `startOrder`, each once its dependencies' deploys are over. `services.depends_on` (JSON `store.Dependency`, by name; renames update them, a dangling one is skipped) changes no container, so it never redeploys. `Runtime.Restart` "no" or "on-failure" makes a job: Docker's restart policy, ready (deploys, health) when exited 0 — and for "no" only then —, never restarted by the reconciler. A missing `${NAME}` that is a whole env value becomes `{{ project.NAME }}`, unless it is an existing secret's export placeholder, which keeps its value. One apply per project at a time.
- **Reference:** `site/docs/compose.md` is the format's documentation, published on the website; the MCP server's instructions point agents to it. A test fails when a key the parser reads or drops isn't documented, or an example doesn't parse.
- **Templates** (`internal/templates`, issue #78): one-click apps are folders embedded in the binary (`files/<id>/compose.yaml`, with a top-level `x-template` block: title, category, tags, icon, inputs; optionally `logo.svg`, served by `GET /api/templates/logos` so the UI needs no code per template). The demo's catalog is generated from them (`go generate ./internal/templates`, without image tags so Dependabot's bumps don't touch it). Installing renders the inputs (asked, or `generate`d with `randomToken`) into the `.env` and goes through `ApplyCompose` with deploy, never prune: into an existing project only when no listed service exists there yet, or into a new project created only after a dry run against the unsaved project passes. API `GET /api/templates`, `POST /api/templates/{id}/install`; MCP `list_templates`, `install_template`. Docs: `site/docs/templates.md`.
- **Git:** `project_git` links a project to a branch and path over HTTPS, read through a git provider for a private repository. `git_providers` (`internal/gitprovider`): a GitHub App created from a manifest and installed by the user (installation tokens minted in memory, app-wide push webhooks when the manager is public), or a GitLab/Gitea OAuth application (tokens stored, refreshed). A provider's token only goes to its own host. A loop polls with `ls-remote` (no fetch) and, on a new commit, shallow-clones into a temp dir under `$DATA_DIR/git` (disk, not RAM), reads the file and applies it with pruning; push webhooks and "sync now" force it. Linking shows a dry run first. While linked, service create/update/delete outside the sync is refused (`ErrGitManaged`), except deleting an orphaned database. Each service stores the hash of its compose block: a sync skips a service whose block is unchanged, so a tag deployed by CI (deploy token) stays until the file changes that service. The images the last sync set are kept to show such drift. Outcomes go to `git.sync.failed` / `git.sync.succeeded`.

### 9.2 Preview environments

A **preview** is a regular project (`projects.parent_id`, `projects.preview` JSON: name, branch, commit, TTL, last deploy) copied from a parent, named `<parent>-<name>`; it reuses networks, deploys, logs and pages. The manager knows nothing about pull requests or stacks: CI picks the name (one per stack of PRs, see `site/docs/previews.md`) and calls `DeployPreview` (`internal/core/previews.go`) with image tags.

- **Source:** a git-linked parent's compose file at the given branch (`gitReadFile`, `${NAME}` → `{{ project.NAME }}`), else the parent exported (`exportFile`), its service secrets turned into `{{ project.<KEY> }}`. Applied with `applyCompose` (prune, deploy) into the preview; the preview is not git-linked itself.
- **Changes:** apps on `<name>-<service>.<base domain>` (`<name>.<base>` with one routed app; one DNS label so one wildcard record/cert covers them, and prod cookies on the parent domain never reach previews), the preview project sleeps (30 min default, off without a routed app or a manager domain on a remote server), `X-Robots-Tag: noindex`, one replica; published ports, host-access apps, uptime checks and database passwords dropped (fresh databases).
- **Env (never prod's by default):** per-parent `preview_settings` (base domain, env + secrets, `inherit` list, TTL, sleep). The preview's project variables are the parent's plain variables, the inherited secrets, then the settings', then CI's (non-secret). A parent secret or password-manager reference is left out unless inherited. A missing value is refused, not filled from prod; a resolved value naming a parent domain is refused unless the settings set it. `{{ app.X.URL }}` resolves to the preview's own apps.
- **Seed** (`--seed`, only if the settings allow it: production data): the databases a deploy creates get the parent's latest successful dump of the same database, restored (`RestoreBackup` into the preview's service) after the databases deploy and before the apps (`ApplyOptions.afterDatabases`, in `deployInOrder`), so migrations run on the seeded schema. Refused up front when a database has no backup; later deploys keep the preview's data.
- **Anonymized seeds** (`preview_settings.seed_mode`: off, `anonymized`, `raw`; `allow_seed` became `raw`): each PostgreSQL/MySQL/MariaDB/MongoDB service has rules (`services.anonymize`, JSON `anonymize.Rules`; NULL none, `{}` nothing personal; from `x-kipitiny.anonymize` in git projects, else the settings). `internal/anonymize` (pure) turns them into a psql script (`pg_temp` helpers, values cast back to each column's type, `session_replication_role = replica`), a mysql script (helper functions dropped before the dump) or a mongosh script (values through env); `hash`/`fake:` values are salted MD5s of the original (`preview_settings.salt`), identical across dialects (`anonymize.Value` is the reference). `internal/core/anonymize.go` makes an **anonymized copy** of a database's latest backup per rules hash: restored into a throwaway, network-less server (`scratchPostgres`/`scratchDump`, shared with restore tests and `verifySem`), rules applied, dumped next to the backup as a `backups` row with `source_id`/`rules_hash` (hidden from backup lists, deleted with its source; a good copy deletes the older ones). Triggered after a backup, on rules or settings changes, after a compose apply, and hourly (restarts). `--seed` in anonymized mode restores the latest good copy with the current hash and refuses otherwise: raw data is never the fallback.
- **Lifecycle:** CI closes it (`ClosePreview`), or the expiry loop does after the TTL without a request (sleep's activity tracking) or a deploy. Deleting the parent deletes its previews. Members' grants on the parent apply to its previews.

## 10. PostgreSQL management and backups (core feature)

### Provisioning

- One Postgres container per database service, on the private project network only.
- Generated password, named volume for data.
- Docker memory limit and sane defaults (`shared_buffers`, `max_connections`).

### Redis

- Same model as Postgres: one container, one replica, private network only, named volume, password generated at creation and fixed.
- AOF persistence (`appendonly yes`); `maxmemory` at 75% of the container limit so Redis refuses writes instead of being OOM-killed.
- Backed up as a volume (see below): a crash-consistent copy of the AOF, which Redis loads even if its tail is cut.

### MySQL, MariaDB and MongoDB

- Same model again; MySQL and MariaDB share code (same environment, protocol and dump format; MariaDB's images only ship the `mariadb-*` client names). An app user owns database `app`; root's password is generated too and only used by the manager. MongoDB's user is the instance's root (`authSource=admin`), so apps may use other databases.
- Sizing from the memory limit: InnoDB buffer pool at half of it (performance schema off, it costs a few hundred MB); WiredTiger cache like `mongod` sizes it from a machine's RAM. Mongo's `/data/configdb` VOLUME is a tmpfs, so recreates leave no anonymous volumes.
- Healthchecks must not pass on the images' init-time temporary servers: MySQL's runs with `--skip-networking` (so ping over TCP), Mongo's is forked by the entrypoint before it `exec`s the real one (so PID 1 must be `mongod`).
- Backed up as dumps (backup kind `dump`, the backup's service kind tells which): `mysqldump --single-transaction` gzipped by the manager, `mongodump --archive --gzip`, through the same exec→upload path as `pg_dump`. A tar of their live data files would not be consistent. Restores read the backup through first (checksum, decryption, gzip); MySQL then loads into a scratch database and, once that worked, into the recreated live one (no cross-database rename for views and triggers); Mongo uses `mongorestore --drop`, skipping `admin`, `config`, `local`. Restore tests load into a throwaway server of the service's image.
- Compose only infers them from `x-kipitiny.kind`, not from the image: projects already ran these images as apps.

### Backup method

Run `pg_dump -Fc` **inside the database container** via `docker exec` so the dump tool matches the server version and the manager image needs no Postgres tools. Demux the exec stream with `stdcopy.StdCopy` into an `io.Pipe`, and stream it to S3 with `PutObject(size=-1)` (constant memory, no temp files). After upload, `ContainerExecInspect`: if exit code ≠ 0, delete the object and fail with stderr (pg_dump can fail after data started flowing). `-Fc` is already compressed and supports selective restore.

### What "clean backups" means

- **Check exit codes** and delete partial uploads.
- **Retention policies** (e.g. 7 daily, 4 weekly, 6 monthly), pruned automatically.
- **Metadata in SQLite:** size, duration, Postgres version, checksum, status. A copy goes next to each successful backup as `<object>.json` (the row without this manager's IDs, schedule or restore test; age-encrypted when the target is), deleted with it; manager snapshots get none. *Scan* on a target adopts the ones the manager doesn't know (issue #424).
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

### Volume files

Every service's volumes can be browsed from the UI, the API and MCP: an app's named volumes, a database's data volume. Apps' are read-write; databases' are read-only (dumps, restores and the data browser cover real needs; editing a data dir by hand breaks it), and core refuses writes there whatever the caller.

- Same helper as backups (`busybox`, no network, `kipitiny.component=volume-helper`, no `kipitiny.service` label so the reconciler never counts it as a replica), mounting the service's volumes under `/v/<name>` (`:ro` for databases). One helper per service, started on first use and kept while used: removed after 5 minutes idle, when its volumes change, before the service's volumes are deleted, and on shutdown. Leftovers go with the startup cleanup.
- Paths are `<volume>/<relative path>`; the empty path lists the volumes. Core rejects `..`, absolute paths and unknown volumes, and the helper script resolves symlinks (`realpath`) and refuses anything outside `/v/<volume>`. Listing shows symlinks with their target without following them.
- Commands run over `docker exec` with paths as arguments (`sh -c script sh "$@"`), never interpolated. Listing: `stat` per entry, then the name NUL-terminated (names may hold newlines). Reading a text file: up to 1 MiB, refused when binary. Download: a file's bytes as they are, a folder or several entries as `tar -czf -` streamed to the response.
- Permissions: listing, reading and downloading need `files` (files may hold secrets); writes need `files-write` and are audited with the path.
- Writes: create folders (parents one level at a time, refusing a symlink out of the volume), write a file, move or rename and copy (across the service's volumes too), delete (every path checked before anything goes). An upload streams into a temporary file next to its target, moved into place only once the byte count matches what was sent: a cut upload changes nothing. What's created belongs to the owner of its folder (an image running as uid 1000 can use it); a replaced file keeps its owner and mode. An editor's save passes the mtime it read, and a file changed since is a conflict. Symlinks are resolved in parent folders only, so deleting or moving a link acts on the link.
- The service keeps running: files are read live (a database's data dir copied this way isn't a consistent backup; the UI says so).
- A container's own filesystem is browsed too: `@container/<absolute path>` is the first running replica of the serving deployment, `@<container ID>/...` a given one (volume names can't hold `@`). The same scripts run in that container over `docker exec` as root (`User: 0`, whatever the image's user), rooted at `/`. They need `sh`, `stat`, `realpath` and `cat` (busybox or coreutils); images without them (distroless, scratch) get a clear error. Writes are allowed there (read-only for databases) with a warning in the UI: they reach one replica and are lost on the next deploy or recreate. Moves and copies don't cross between a container and the volumes, which run in different places.

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

### Upgrade notices

A release that disrupts running servers on upgrade (Traefik recreated, a manual step) says so in `UPGRADE.md` (`## Unreleased` → `## X.Y.Z` at release; `- warning:` / `- info:` items, `internal/upgrade`). The release workflow prepends the version's notices to the GitHub release notes and puts all of them, as JSON, in the image label `io.kipitiny.upgrade-notices`. The update check reads the latest image's labels from the registry (manifest index → this platform → config) and keeps the notices of the versions after the running one up to latest; they go in the update-available notification (warning level when one is) and the UI, whose update dialog wants a confirmation when one is a warning.

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

- `internal/notify` is transport-agnostic: an `Event` (type, level, title, message, fields, link) and a `Sender` per channel kind. Each kind declares its config fields (secret or not, free text or a choice whose options core fills in, like the email providers), so the UI renders new kinds without changes.
- Kinds: Discord webhook, email (recipients and an email provider of §Email per channel) and push; generic webhooks later. A sender gets its transports as `notify.Deps` (HTTP client, mail func), so `notify` stays ignorant of settings.
- Push (Web Push, `internal/notify/webpush.go`, stdlib crypto + `golang-jwt`, no push library): the payload is encrypted for the browser (RFC 8291 aes128gcm) and signed with the manager's VAPID key (RFC 8292, generated on first use, setting `webpush`). Admins only (events cover every project): `push_subscriptions` holds each admin's browsers (Account), endpoints limited to known push services' hosts so a subscription can't make the manager call anything else; a push channel delivers to all of them, deleting those the push service answers 404/410 for. `web/public/sw.js` handles only `push` and `notificationclick`: no fetch handler, nothing cached, so the UI never outlives an upgrade. The UI is installable: `/api/manifest.webmanifest` (public) follows the branding.

### Email

- `internal/mail` sends through SMTP servers the admin sets in Settings › Email (stdlib `net/smtp`, PLAIN or LOGIN sign-in, STARTTLS/TLS/none). No mail server in the manager: deliverability needs a sender reputation it can't have.
- Several servers, `email_providers` (issue #329, password masked on read), so invites and each notification channel can send through their own (another sender, another account's quota). Invites use the one the `email_invites` setting names, else the oldest; an email channel names its provider in its config, and a provider can't be removed while a channel uses it. Saving connects and signs in, nothing sent. The single `smtp` setting of earlier versions becomes a provider at startup, used by invites and the existing email channels.
- Messages are multipart text + HTML from one layout (`mail.Body`): notification channels and invites render through it.
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
- Also `x-kipitiny.uptime` in compose: an apply sets the file's check and removes one the file doesn't have, without a deploy. A git project's checks are only set from its file, like its services.

### Web analytics

- First-party tools ship in the manager's image, not their own: off is no container, so no memory. One taking public traffic runs as its own container (`kipitiny <tool>`, no Docker socket, capped), never in the manager's process; an internal-only one may run in the manager, started only while it's on (#429).
- Optional, cookieless (Umami-like). `internal/analytics` is the manager binary's `analytics` command, run from the manager's own image (`KIPITINY_IMAGE` at its version, `:dev` between releases) by the manager on its own server while Settings › Analytics is on (`ensureAnalytics` from the reconciler, like cloudflared): container `kipitiny-analytics` (`kipitiny.component=analytics`, user 65532, read-only root, no capabilities, 256 MB) on `kipitiny-proxy`, volume `kipitiny-analytics` kept when it's turned off (removed by *Delete data*). Events never reach the manager's SQLite: the server keeps its own file, opened through `internal/sqlitedb` like the manager's.
- Public port (Traefik labels on the container, `Host(<analytics domain>)`, DNS synced like an app's): `/a.js` tracker (pageviews, SPA navigations, `kipitiny.track(name, props)`, `data-kipitiny-event` clicks) and `POST /e` (`sendBeacon`, `text/plain`, so no CORS preflight; same 204 whatever happens). Hits for hostnames the manager didn't send are dropped; bots by user agent.
- No cookie or storage: a visitor is `sha256(host, IP, UA, daily salt)`, the salt kept for the day (so restarts don't double count) and then deleted, so nobody is followed across days and raw IPs are never stored. IP from `CF-Connecting-IP`, else Traefik's `X-Real-Ip`; country from `CF-IPCountry` (Cloudflare only, no GeoIP database). Visits end after 30 minutes idle (bounce, duration, entry/exit pages). Query strings keep their keys only (values may be tokens), UTM values aside. `kipitiny.identify(id, props)` sends a `$identify` hit (not counted) that names the current visit, and later hits carry the id: the one way visits are linked across days, the app's choice.
- Private port, bearer token generated by the manager (in the `analytics` setting, passed in the env): `PUT /api/sites` (the domains of apps with `services.analytics`, `x-kipitiny.analytics`; sent from the reconciler when they change) and `GET /api/stats` (summary and the previous span's, series, breakdowns; `filter=<breakdown>:<value>` keeps a pageview column's matching events, or the visits with an entry/exit page, custom event or identity), `GET /api/visits` (newest first, paged by `before`, attributes from each visit's first event) and `GET /api/visits/{id}?host=` (the journey; another host's visit is a 404). The manager calls it at the container's address on the proxy network; other containers there have no token. `GET /api/services/{id}/analytics` (view) and MCP `get_analytics` read it. Events older than the retention (365 days) go hourly.
- Pixels (`/p/<slug>.gif`, a no-store 1x1 GIF, the same for any slug) and tracked links (`/l/<slug>`, 302 to the target, 404 when unknown) are `trackers` rows in the server's SQLite (the source of truth: CRUD at `/api/trackers` on the private port, proxied by the manager's admin-only `/api/analytics/trackers`; *Delete data* removes them). Their hits are events of the pseudo-host `<kind>:<id>`, so stats, breakdowns and filters read them like a site's; bots are redirected but not counted.
- Manager's server only for now; previews never track.

## 13. UI

- React SPA (Vite), React Router, TanStack Query, Tailwind. No Next.js / Node at runtime.
- Embedded in the binary (`web/embed.go`), SPA fallback to `index.html`.
- Dev: Go on `:8080`, Vite dev server proxies `/api`.
- Branding (issue #322, `internal/core/branding.go`): settings `branding` (name, theme, logo type and hash) and `branding_logo` (base64, at most 128 KB, type sniffed: PNG, JPEG, WebP or an `<svg>` root). The auth state carries name, accent and the logo URL (`/api/branding/logo?v=<hash>`, public, sandbox CSP), so the sign-in page has them; the theme (accent and dark-mode accent, tint, radius, font, density: colours `#rrggbb`, the rest enums) becomes a `<style>` appended after `index.css`, overriding its `:root`/`.dark` variables (`--primary`, surfaces mixed with the tint via `color-mix`, `--radius`, `--app-font`, Tailwind's `--spacing`); the UI also sets the tab title and favicon. Presets exist only in the UI and fill the theme in; Settings › Appearance previews the unsaved theme on the whole UI through a context in `Layout`. Emails use the name, and the accent unless the body sets its own (notifications keep their level colour).
- Tags (issue #323, `internal/core/tags.go`): `projects.tags` and `services.tags` (JSON arrays), lowercased, up to 10 of 32 characters. A service's come from `x-kipitiny.tags` like its icon (a git project's file owns them; a change never redeploys), a project's are set on their own. `tag_colors` holds only the colors users picked, from a fixed palette of Tailwind color names; any other tag's color is FNV-1a of its name over the palette, computed the same way by the UI (`web/src/lib/tags.ts`). Lists filter by tag client-side (`?tag=`, every one must match).
- Release: multi-stage Dockerfile → distroless `static` image (the binary is CGO-free).
- Web terminal: `docker exec` with a TTY, bridged to xterm.js (lazy-loaded chunk) over a websocket (`golang.org/x/net/websocket`). Browser sends JSON `input`/`resize` messages; the server sends raw output as binary frames, then one `exit`/`error` message. Admin only, Origin must match the host, every session opened/closed lands in the audit log.
- Data browser (service › Data, Postgres and Redis): the database's own client runs inside its container via `docker exec`, like backups — no driver in the manager, no network path to the database, client matches the server. Postgres: `psql` with `PGOPTIONS=-c default_transaction_read_only=on -c statement_timeout=…`; queries the manager builds select `json_build_array(left(col::text, 4097), …)` so each row is one JSON line (NULLs and newlines unambiguous), every quoted name is checked against the catalog first, `LIMIT n+1` tells whether more follow; CSV export is `COPY (…) TO STDOUT` streamed to the response. MySQL/MariaDB: the same shapes as Postgres (a database is the schema), `mysql -N -B --raw` printing `JSON_ARRAY(...)` lines, values as hex literals with an introducer (safe whatever the SQL mode), the console's output read as `--xml` (NULL vs text unambiguous, odd bytes replaced). MongoDB: mongosh scripts the manager writes, caller input only through the environment, documents as relaxed EJSON lines, the console returning mongosh's printout, mongosh run under `timeout` (it would outlive a cancelled exec). Both browse and run the read-only console as `kipitiny_read` (SELECT / readAnyDatabase), created lazily with a password derived from root's, so the server enforces it. Redis: Lua scripts through `redis-cli EVAL` (SCAN, TYPE/PTTL/MEMORY USAGE, then per-type paging) return `{ok}`/`{err}` JSON, since `redis-cli` exits 0 on server errors. Rows, cells (4 KiB) and output (1 MiB) are capped, never buffered whole. The admin-only console is read-only unless write mode is set (Postgres: `--single-transaction`, drained to the end so capped output never rolls a write back; Redis: commands flagged `write`/`admin` by `COMMAND INFO` refused); blocking commands always refused; runs audited without their text.
- Server terminal: same bridge, into a throwaway privileged `alpine` container (`--pid=host`, labelled `kipitiny.component=terminal`) running `nsenter -t 1 -m -u -i -n -p` then `su -l root`: a root shell on the host, local or remote, with nothing but Docker access (already root-equivalent). Closing the session removes the container; leftovers are cleaned at startup.

## 14. API and MCP

```
            ┌── REST API (web UI, CLI) ──┐
Clients ────┤                            ├──→ core (service layer) ──→ Docker / SQLite
            └── MCP tools (AI agents) ───┘
```

- All logic in `internal/core`. REST handlers and MCP tools are thin adapters. Permissions enforced in one place.
- MCP built in (`/mcp` module), Streamable HTTP at `/mcp`, reusing auth, tokens, permissions, audit logs. Standalone stdio proxy later.
- Dry-run mode (`/mcp?dry_run=true`, a second server with the same tools): read-only tools run, tools with a `dry_run` input have it forced, `query_database` runs without `write`, any other tool returns an error result naming its arguments and runs nothing.
- Task-oriented tools, not 1:1 REST mapping: `deploy_image`, `get_app_status`, `get_logs`, `rollback`, `backup_database`, `restore_database`.
- Mask secrets in every tool response; restricted tokens; confirmation for destructive tools.
- MCP covers what an operator does on projects, services, data, backups and git; server settings (servers, storage, providers, notifications, Cloudflare, cleanup, updates) stay in the UI, and session-only routes (tokens, account, project export with every secret) never reach it.
- No docs tool: the server instructions give agents the docs URLs of their version (§14a) and GitHub's `site/docs` at that tag as a fallback.

## 14a. Website and documentation

- `site/`: landing page and docs, one React Router app prerendered to static HTML (`ssr: false`), served by nginx as `ghcr.io/mathoyer/kipitiny-homepage`. Separate from the manager image: the manager ships no docs and no Node.
- Docs are `site/docs/<slug>.md` (front matter: title, description, order), also served as markdown at `/docs/<slug>/llms.txt`, indexed by `/llms.txt`.
- Built on each release tag from the tagged commit, so `/docs` describes the latest release; the footer shows `VERSION`.
- Versioned: `site/scripts/versions.sh` copies `site/docs` at the last patch tag of each minor since 0.10 to `/docs/<minor>/...` (own `llms.txt`, version picker, banner on old versions). Generated in CI from tags, not committed.
- The manager UI and MCP instructions link to the manager's own minor (`/docs/0.10/...`; a dev build gets the latest); old `/docs/*` URLs redirect there.

## 15. Multi-server

Shipped: remote Docker daemons over SSH (manager key, host key pinned on first use). A project lives on one server, fixed at creation; each server runs its own Traefik and optional tunnel, and DNS points a domain at its project's server. Each database lives on one known server; backup logic unchanged.

**No Swarm services.** Swarm volumes are node-local, so databases would be pinned with constraints anyway; backups would still have to find the task's node and exec through its daemon; and the reconciler, rollouts, logs, terminal and probes would be rewritten for the service/task API. Single-server installs (most of them) would carry raft, ingress and VXLAN for nothing.

### Replicas across servers (later, issue #79)

Deferred until there is a concrete need. Planned approach: one edge Traefik spreads traffic across workers over a private network; each worker keeps its own Traefik routing to its local replicas with today's labels.

```
client → edge (tunnel or 80/443) → edge Traefik → private network → worker Traefik → app container
```

1. **Server roles:** `standalone` (default, today's behaviour), `edge` (Traefik receiving public traffic, generated routes, no apps) or `worker` (apps + a Traefik bound to the private IP only, no public ports). Each edge and worker has a private IP (phase 1: bring your own, from the provider's VLAN, Tailscale or WireGuard; later a manager-run WireGuard mesh). If the network isn't encrypted, the edge talks HTTPS to workers with certificates the manager issues.
2. **Edge exposure is the server's existing setting, not part of the role.** With a tunnel token, the edge publishes no ports and runs no ACME, as today. Without one, it publishes 80/443 and gets certificates as a standalone server does; the HTTP/TLS challenge works with one edge. Two edges: with a tunnel, they share the token (cloudflared replicas, Cloudflare fails over); public, they require a Cloudflare zone (DNS-01: the TLS challenge breaks with several A records), and failover between them is the operator's (floating IP); round-robin A records alone don't health-check. DNS sync points a spread service's domain at the edges (one A per public edge, or CNAME + route to the shared tunnel), never at workers.
3. **Placements:** `service_placements (service_id, server_id, replicas)`; no rows = all replicas on the project's server. Only workers can be placed. The reconciler iterates placements; containers carry `kipitiny.server`; the project network is created on every placed server. Placing an app with a local volume on several servers is refused. Databases stay on the project's server, 1 replica, published on its private IP only; `{{ db.X.host }}` resolves to that IP:port for replicas on other servers.
4. **Edge routes** are generated by the manager from the store (Traefik file or HTTP provider): host → each placed worker's private IP, with a health check that drops a dead worker within seconds, optional sticky cookie. Regenerated on placement, domain or drain changes. Per-app middlewares stay on the worker Traefik; workers trust `X-Forwarded-For` from the edges' private IPs only, and the middleware `ipStrategy.depth` counts the edge hop.
5. **Rollouts** go worker by worker: drain it from the edge pool, deploy with the health gate, add it back. A failure stops the rollout and rolls already-updated workers back to the previous digest. A **Drain server** action reuses it for host maintenance.
6. **Failover** (optional, later): replicas of an unreachable worker restarted on another placed worker after a delay, by the reconciler.

Open: edge pulls its routes over HTTP (keeps the last config while the manager is down) or the manager pushes them over SSH; whether the manager may run on a worker.

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
13. Built-in data browser for every database kind, also as MCP tools (`read_table`, `find_documents`, `query_database` read-only unless `write`).
14. Several users: admins and members with per-project permissions, invite links, member-owned tokens.

## 17. Reference projects

- **PG Back Web** (`eduardolat/pgbackweb`, Go): backup UX reference.
- **nuelScript/skiff** (Go): single-binary Docker PaaS with SQLite and label routing; very similar.
- **Kamal**: zero-downtime deploys without orchestration.
- **Dokku**: lightweight, mostly shell.
