# kipitiny

Lightweight self-hosted PaaS: Docker apps + PostgreSQL with clean, verified backups.
Single Go binary with an embedded React UI, SQLite for state, Traefik for routing.

See [docs/design.md](docs/design.md) for the full design.

## Run

To set up a fresh server (firewall, SSH hardening, Docker, kipitiny), use the
Ansible playbook in [`deploy/ansible`](deploy/ansible/README.md). By hand, only
this repo's `docker-compose.yml` is needed on the server:

```sh
docker compose up -d   # http://localhost:3000
```

The manager controls the host Docker daemon through the mounted socket.
**Access to the socket is root on the host**, so the UI and API require a login.

On first start the manager logs a one-time **setup token**
(`docker compose logs manager | grep setup_token`); the UI asks for it to create
the admin account. Forgot the password?

```sh
docker compose exec -it manager /kipitiny reset-password admin
```

## Upgrading

The sidebar shows **Update available** when a newer version is published
(checked every 6 hours); **Update now** installs it. Or from the shell:

```sh
docker compose pull && docker compose up -d
```

Either way only the manager restarts: apps, databases and Traefik keep running.
Running deploys and backups finish first (up to 5 minutes), and before database
migrations the manager keeps a copy of its state as
`/data/kipitiny.db.pre-migrate-<version>` (last 3 kept).

The UI update pulls the new image and starts a short-lived `kipitiny-updater`
container that swaps the manager's container, keeping its configuration,
volumes and networks. If the new version isn't healthy within 3 minutes, the
previous one is restored; the updater's output ends up in the manager's log.
It needs the manager to run in Docker. With a compose file pinning
`KIPITINY_VERSION`, the button is off (the next `up` would go back to that
version): change the variable and run `docker compose up -d` instead.
Settings › Version checks for a new release on demand.

### Releasing

Push a tag `x.y.z` (no `v`): GitHub Actions publishes
`ghcr.io/mathoyer/kipitiny:x.y.z` and `:latest` for amd64 and arm64, and a
GitHub release. After the first release, make the package public (GitHub →
Packages → kipitiny → Package settings), or managers can neither see nor pull
updates.

## Services

- **Apps** run from an image, optionally public on a domain (HTTPS via Traefik), 1–10 replicas.
- **PostgreSQL** services get generated credentials, a named data volume, a memory
  limit with matching `shared_buffers`, and a `pg_isready` healthcheck. They are
  only reachable inside their project, at `<service-name>:5432`. Link an app to a
  database and it receives `DATABASE_URL` (an explicit `DATABASE_URL` env var wins).
- Deleting a database or a project destroys data and must be confirmed by typing its name.

## Builds from Git

An app can come from a Git repository instead of an image: every deploy clones
the branch (pure Go, HTTPS, optional access token for private repos) and builds
its Dockerfile with BuildKit through a short-lived `docker:cli` container that
receives the source as a tar stream, so neither the host nor the manager needs
`git` or the Docker CLI. Images are tagged per deployment (the last 5 are kept
for rollback) and removed with the service.

**Deploy on push**: point a webhook at `/api/hooks/<service-id>` with the
service's secret: GitHub (`X-Hub-Signature-256`), GitLab (`X-Gitlab-Token`) or
`Authorization: Bearer <secret>`. Pushes to other branches are ignored.

## Deploys

Apps deploy blue-green: every new replica starts next to the old ones, and old
replicas are only stopped once *all* new ones are ready. If one crashes or never
gets ready, the new containers are removed and the old version keeps serving.

- **Ready** means the image's own `HEALTHCHECK` passes, or else an injected probe
  (the manager's static binary, mounted read-only) sees the port accept
  connections or the *health path* answer `< 400`. Traefik only routes to healthy
  containers, so traffic never reaches a replica that isn't serving. Services
  without a port just need to stay up for 5 s.
- A **pre-deploy command** (e.g. migrations) runs once with `sh -c` in a one-off
  container before any replica starts; a failure aborts the deploy.
- Old replicas are stopped one by one and kept (stopped) until the next deploy so
  their logs stay readable. **Roll back** redeploys an earlier image.
- Databases are recreated in place (a volume can't be shared by two servers).

A **reconciler** keeps Docker matching the store every 30 s, shortly after any
change, and whenever a managed container dies or is removed: missing replicas
are recreated from the deployed version, externally stopped ones restarted,
extra ones removed (so changing the replica count applies without a deploy),
and containers of deleted services cleaned up. A service stopped from the UI
stays stopped. Services busy with a deploy, backup or restore are left alone.

## Backups

- `pg_dump -Fc` runs **inside** the database container (client always matches the
  server) and streams straight to the target: local disk (`/data/backups`) or any
  S3-compatible bucket. Memory stays constant; nothing touches a temp file.
- A backup only counts once `pg_dump` exits 0; partial uploads are deleted. Each
  backup records size, SHA-256, server version and duration.
- Restores load the dump into a scratch database and swap it in by rename, so the
  result is exactly the backup and a failed restore leaves live data untouched.
  Apps linked to the database are stopped meanwhile. The checksum is verified.
- Backups outlive their database and project; delete them explicitly.
- **Schedules** (cron, UTC unless `CRON_TZ=` is given) back up a database to a
  target and then apply retention to *their own* backups: keep the last N, plus
  the newest of each of the last N days / ISO weeks / months. Manual backups are
  never pruned. A run that finds the database busy (deploy, restore) retries for
  15 minutes.

### Restore tests

A backup nobody restored is a hope. *Verify* (or a schedule with restore tests on,
the default) restores a backup into a throwaway PostgreSQL container with no
network, matching the backup's major version, then runs `ANALYZE` and records
tables, estimated rows, database size and duration on the backup. The container
and its volume are always removed; one test runs at a time.

### Encryption

An S3 target can encrypt everything stored on it with [age](https://age-encryption.org)
(chosen at creation; the key never changes). Copy the key (*Show key*) somewhere
safe: without it, those backups are unreadable if this server is lost. Offline:

```sh
age -d -i key.txt shop-20260930T030000Z-xxxx.dump.age | pg_restore -d "$DATABASE_URL" --no-owner
```

### Manager state

The manager's own SQLite file (projects, services, schedules, credentials, keys)
is snapshotted with `VACUUM INTO` daily to local disk and the last 14 are kept
(`KIPITINY_MANAGER_BACKUP_CRON` / `_TARGET` / `_KEEP`, `off` to disable). These
snapshots contain every secret the manager holds: prefer an encrypted target.
To restore one, stop the manager and replace `/data/kipitiny.db` with the file
(delete `kipitiny.db-wal` and `kipitiny.db-shm` first).

S3 targets are checked (a test object is written and deleted) before they are saved.
`go test ./internal/storage/` runs S3 tests against a real server when
`KIPITINY_TEST_S3_ENDPOINT` is set (see `internal/storage/s3_test.go`).

## Multiple servers

*Settings → Servers* adds remote Docker hosts over SSH. The manager reaches the
remote Docker **socket** through an SSH channel (pure Go, no `ssh` binary, the
daemon never listens on TCP) with its own ed25519 key, shown in the form to add
to `authorized_keys`. The server's host key is pinned on first connection.
The remote sshd must allow `AllowTcpForwarding yes` (or `local`) and
`AllowStreamLocalForwarding yes` (Debian/Ubuntu defaults; Alpine disables it).

A project lives on one server (its services share a private network); choose it
when creating the project. Each server gets its own Traefik, so point a domain's
DNS at the server running the app. Deploys, builds, backups (streamed back
through SSH to any target), restore tests, logs and the reconciler all work the
same on every server. The injected health probe is copied to each server once
(it needs the same CPU architecture as the manager; otherwise replicas are
gated on staying up instead).

## Cleanup

*Settings › Cleanup* frees disk space on every server, on a cron schedule
(off by default) or with **Run now**. Each part is optional:

- **Images**: dangling layers only, or every image no container uses.
- **Volumes**: unused anonymous volumes, or unused named ones too (careful:
  that includes stopped stacks on the host that kipitiny doesn't manage).
- **Build cache** of Git builds, **stopped containers** and **unused networks**
  that kipitiny didn't create.
- **Deployment history**: keep the last N deployments (and their build logs)
  per service.

Nothing younger than the minimum age (24 h by default) is touched. Whatever the
settings, kipitiny keeps database volumes, everything it labels
`kipitiny.managed`, each service's image and those of its last successful
deployments (rollback), and the current and running deployments. Health probe
volumes left by older manager versions are removed once unused.

## API tokens, MCP and audit

Create tokens in *Settings*. Scopes: **read** (status, logs, backup lists),
**deploy** (plus deploy, rollback, start/stop, back up) and **admin**
(everything, including settings, revealed secrets and restores). Tokens work as
`Authorization: Bearer kpt_…` on `/api` and on the built-in **MCP** endpoint
(Streamable HTTP):

```sh
claude mcp add --transport http kipitiny https://kipitiny.example.com/mcp \
  --header "Authorization: Bearer kpt_…"
```

Tools are task-oriented rather than a copy of the REST API: `list_services`,
`get_app_status`, `get_logs`, `deploy`, `deploy_from_git`, `rollback`,
`backup_database`, `list_backups`, `restore_database` (admin, requires the
database name as confirmation). Secrets are masked in every response. Every
mutation, from the UI, a token or an agent (including refused attempts), lands
in the audit log (kept 90 days).

## Develop

Requires Go 1.26+, Node 24+, pnpm 10.

```sh
make dev-api   # Go API on :8080 (data in ./data)
make dev-ui    # Vite on :5173, proxies /api to :8080
make test      # go test ./...
make lint      # go vet + tsc
make build     # UI + static binary in bin/
make docker    # images `kipitiny` and `ghcr.io/mathoyer/kipitiny:dev`
KIPITINY_VERSION=dev docker compose up -d   # run that build
```

## Configuration

| Env var              | Default | |
|----------------------|---------|---|
| `KIPITINY_ADDR`      | `:3000` | HTTP listen address |
| `KIPITINY_DOMAIN`    | — | Serve the UI/API over HTTPS on this domain, through Traefik |
| `KIPITINY_IMAGE`     | `ghcr.io/mathoyer/kipitiny` | Where new versions are looked up and pulled |
| `KIPITINY_UPDATE_CHECK` | `on` | `off` stops looking for new versions |
| `KIPITINY_DATA_DIR`  | `/data` | SQLite DB, deploy logs, local backups. Local disk only. |
| `KIPITINY_LOG_LEVEL` | `info`  | `debug`, `info`, `warn`, `error` |
| `KIPITINY_SETUP_TOKEN` | random | Fix the first-run setup token instead of generating one |
| `KIPITINY_TRAEFIK`   | `true`  | Run and maintain the Traefik container (`false` to bring your own) |
| `KIPITINY_TRAEFIK_IMAGE` | `traefik:v3.7` | |
| `KIPITINY_HTTP_PORT` / `KIPITINY_HTTPS_PORT` | `80` / `443` | Host ports Traefik binds |
| `KIPITINY_ACME_EMAIL` | — | Let's Encrypt account email (optional) |
| `KIPITINY_CLOUDFLARE_TUNNEL_TOKEN` | — | Receive traffic through a Cloudflare Tunnel instead of ports 80/443 |
| `KIPITINY_CLOUDFLARED_IMAGE` | `cloudflare/cloudflared:2026.9.3` | |
| `KIPITINY_MANAGER_BACKUP_CRON` | `@daily` | Snapshot of the manager's state (`off` to disable) |
| `KIPITINY_MANAGER_BACKUP_TARGET` | `local` | Target ID for those snapshots |
| `KIPITINY_MANAGER_BACKUP_KEEP` | `14` | Snapshots kept per target |
| `KIPITINY_BUILDER_IMAGE` | `docker:cli` | Image running `docker build` for Git services |
| `KIPITINY_DOCKER_SOCKET` | `/var/run/docker.sock` | Host socket path mounted into Traefik |
| `DOCKER_HOST`        | socket  | Standard Docker client env vars apply |

Traefik is (re)created on startup whenever its configuration changes; it keeps
running across manager restarts. HTTP redirects to HTTPS, certificates come from
Let's Encrypt (TLS-ALPN challenge, so port 443 must be reachable publicly).

### HTTPS for the manager

Set `KIPITINY_DOMAIN=kipitiny.example.com` (DNS pointing at the server) and the
manager's Traefik routes that domain to the UI with a Let's Encrypt certificate,
like any app. In a container, the manager attaches itself to the proxy network,
so port 3000 no longer needs to be published: drop it from the compose file or
bind it to `127.0.0.1` for SSH-tunnel access. Run on the host, the manager is
reached through `host.docker.internal`, so it must listen on the Docker bridge
(the default `:3000` does).

### Cloudflare Tunnel (no open ports)

To keep ports 80/443 closed, let traffic come in through a
[Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/):

1. In Cloudflare Zero Trust, create a tunnel (Networks → Tunnels, type
   *cloudflared*) and copy its token.
2. Either connect Cloudflare in *Settings* (below), which adds each service's
   hostname to the tunnel, or add public hostnames yourself: `example.com` and
   `*.example.com`, both with service **HTTPS** `kipitiny-traefik:443`, and
   under TLS enable **No TLS Verify** and **Match SNI to Host**. Check that DNS
   has a proxied `* CNAME <tunnel-id>.cfargotunnel.com` record, and add it if
   the dashboard didn't.
3. Paste the token in *Settings › Servers › Network* (globe button), or start
   the manager with `KIPITINY_CLOUDFLARE_TUNNEL_TOKEN=<token>` for its own
   server (with `KIPITINY_DOMAIN`), and publish no port in the compose file.

The manager then runs `kipitiny-cloudflared` next to Traefik, and Traefik
publishes no ports and requests no Let's Encrypt certificates: Cloudflare
serves the public certificate, and Traefik's own certificate only protects the
hop from cloudflared. Service domains need no per-app setup as long as they
match a hostname of the tunnel. Cloudflare's free certificate covers one level
of subdomain (`app.example.com`, not `a.b.example.com`).

Each server can have its own tunnel (one token per server; create one tunnel
per server): the manager runs cloudflared on every server with a token, and
those servers publish no ports. With Cloudflare connected, each app's hostname
points at the tunnel of the server running it. Apps deployed before switching keep working, but redeploy them to drop
their Let's Encrypt labels (Traefik logs a "nonexistent certificate resolver"
error until then).

### Cloudflare DNS

List your domains in *Settings › Domains*, then connect Cloudflare in
*Settings › Cloudflare* with an API token (*My Profile › API Tokens*) allowed
**Zone › Zone › Read** and **Zone › DNS › Edit**, plus **Account › Cloudflare
Tunnel › Edit** with a tunnel. For every service domain in one of the token's
zones, the manager then keeps the DNS record in sync:

- an **A** record to the server's public IP (detected, or set under
  *Settings › Servers*), proxied if the domain is marked **Proxied**;
- behind the tunnel, a proxied **CNAME** to it, and the tunnel's route to
  Traefik.

Records are created, updated and removed as services come and go; they carry
the comment `managed by kipitiny`, and records without it are never changed (a
service whose name already has one shows a DNS conflict). Certificates for these
domains come from Let's Encrypt through the Cloudflare DNS challenge, so they
work behind the proxy too; Traefik gets the token for that. Proxied domains
need the zone's SSL/TLS mode on **Full (strict)**. Services deployed before
connecting keep their certificate settings until their next deploy.

For local testing use a `*.localhost` domain and alternate ports, e.g.
`KIPITINY_HTTP_PORT=8081 KIPITINY_HTTPS_PORT=8443`, then
`curl -k https://app.localhost:8443` (Traefik serves its default self-signed cert).

## Layout

```
cmd/kipitiny/          entrypoint
internal/config/       env config
internal/core/         service layer (REST + future MCP call this)
internal/api/          JSON HTTP handlers
internal/docker/       Docker client wrapper, label/network conventions
internal/store/        Store interface + models
internal/store/sqlite/ SQLite impl (bun + goose migrations)
web/                   React SPA (Vite), embedded via go:embed
```
