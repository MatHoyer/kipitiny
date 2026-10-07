# kipitiny

Lightweight self-hosted PaaS: Docker apps and databases with clean, verified backups.
Single Go binary with an embedded React UI, SQLite for state, Traefik for routing.

**Documentation: https://kipitiny.mathieuhoyer.fr/docs** (sources in
[`site/docs`](site/docs)). See [docs/design.md](docs/design.md) for the design.

## Run

Only this repo's `docker-compose.yml` is needed on the server:

```sh
curl -fsSLO https://raw.githubusercontent.com/MatHoyer/kipitiny/main/docker-compose.yml
docker compose up -d   # http://localhost:3000
docker compose logs manager | grep setup_token
```

The UI asks for that one-time setup token to create the admin account. To set
up a fresh server (firewall, SSH hardening, Docker, kipitiny), use the Ansible
playbook in [`deploy/ansible`](deploy/ansible/README.md). Everything else is in
the [documentation](https://kipitiny.mathieuhoyer.fr/docs).

## Develop

Requires Go 1.26+, Node 24+, pnpm 10.

```sh
make dev-api   # Go API on :8080 (data in ./data)
make dev-ui    # Vite on :5173, proxies /api to :8080
make test      # go test ./...
make lint      # go vet + tsc
make build     # UI + static binary in bin/
make docker    # images `kipitiny` and `ghcr.io/mathoyer/kipitiny:dev`
make dev-site  # the website (landing page and docs) on :5173
KIPITINY_VERSION=dev docker compose up -d   # run that build
```

Docs are `site/docs/<slug>.md`: a new file is a new page. `go test
./internal/compose/` checks that `site/docs/compose.md` documents every key the
parser reads.

## Releasing

Push a tag `x.y.z` (no `v`): GitHub Actions publishes
`ghcr.io/mathoyer/kipitiny:x.y.z` and `:latest` for amd64 and arm64, and a
GitHub release; the website image `ghcr.io/mathoyer/kipitiny-homepage` is rebuilt from the same tag, so its docs describe the latest release (run the *site* workflow by hand for a site-only fix). After the first release, make the package public (GitHub →
Packages → kipitiny → Package settings), or managers can neither see nor pull
updates.

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
site/                  website: landing page and docs (site/docs/*.md), own image
```
