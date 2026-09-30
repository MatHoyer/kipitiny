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
