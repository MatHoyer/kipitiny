# kipitiny

Lightweight self-hosted PaaS: Docker apps + PostgreSQL with clean, verified backups.
Single Go binary with an embedded React UI, SQLite for state, Traefik for routing.

See [docs/design.md](docs/design.md) for the full design.

## Run

```sh
docker compose up -d --build   # http://localhost:3000
```

The manager controls the host Docker daemon through the mounted socket.
**Access to the socket is root on the host** — put strong auth in front of it.

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
| `DOCKER_HOST`        | socket  | Standard Docker client env vars apply |

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
