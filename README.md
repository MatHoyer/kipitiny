# kipitiny

Lightweight self-hosted PaaS: Docker apps + PostgreSQL with clean, verified backups.
Single Go binary with an embedded React UI, SQLite for state, Traefik for routing.

See [docs/design.md](docs/design.md) for the full design.

## Run

```sh
docker compose up -d --build   # http://localhost:3000
```

The manager controls the host Docker daemon through the mounted socket.
**Access to the socket is root on the host**, so the UI and API require a login.

On first start the manager logs a one-time **setup token**
(`docker compose logs manager | grep setup_token`); the UI asks for it to create
the admin account. Forgot the password?

```sh
docker compose exec -it manager /kipitiny reset-password admin
```

## Services

- **Apps** run from an image, optionally public on a domain (HTTPS via Traefik), 1–10 replicas.
- **PostgreSQL** services get generated credentials, a named data volume, a memory
  limit with matching `shared_buffers`, and a `pg_isready` healthcheck. They are
  only reachable inside their project, at `<service-name>:5432`. Link an app to a
  database and it receives `DATABASE_URL` (an explicit `DATABASE_URL` env var wins).
- Deleting a database or a project destroys data and must be confirmed by typing its name.

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

## Develop

Requires Go 1.26+, Node 24+, pnpm 10.

```sh
make dev-api   # Go API on :8080 (data in ./data)
make dev-ui    # Vite on :5173, proxies /api to :8080
make test      # go test ./...
make lint      # go vet + tsc
make build     # UI + static binary in bin/
make docker    # image `kipitiny`
```

## Configuration

| Env var              | Default | |
|----------------------|---------|---|
| `KIPITINY_ADDR`      | `:3000` | HTTP listen address |
| `KIPITINY_DATA_DIR`  | `/data` | SQLite DB, deploy logs, local backups. Local disk only. |
| `KIPITINY_LOG_LEVEL` | `info`  | `debug`, `info`, `warn`, `error` |
| `KIPITINY_SETUP_TOKEN` | random | Fix the first-run setup token instead of generating one |
| `KIPITINY_TRAEFIK`   | `true`  | Run and maintain the Traefik container (`false` to bring your own) |
| `KIPITINY_TRAEFIK_IMAGE` | `traefik:v3.7` | |
| `KIPITINY_HTTP_PORT` / `KIPITINY_HTTPS_PORT` | `80` / `443` | Host ports Traefik binds |
| `KIPITINY_ACME_EMAIL` | — | Let's Encrypt account email (optional) |
| `KIPITINY_MANAGER_BACKUP_CRON` | `@daily` | Snapshot of the manager's state (`off` to disable) |
| `KIPITINY_MANAGER_BACKUP_TARGET` | `local` | Target ID for those snapshots |
| `KIPITINY_MANAGER_BACKUP_KEEP` | `14` | Snapshots kept per target |
| `KIPITINY_DOCKER_SOCKET` | `/var/run/docker.sock` | Host socket path mounted into Traefik |
| `DOCKER_HOST`        | socket  | Standard Docker client env vars apply |

Traefik is (re)created on startup whenever its configuration changes; it keeps
running across manager restarts. HTTP redirects to HTTPS, certificates come from
Let's Encrypt (TLS-ALPN challenge, so port 443 must be reachable publicly).

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
