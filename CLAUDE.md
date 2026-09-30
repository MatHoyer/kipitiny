# kipitiny

Design doc: `docs/design.md` — read before architectural changes. Roadmap in §16.

## Rules
- Manager is ONE process: no Postgres/Redis/worker containers for its own state.
- RAM budget matters: no Node at runtime, no heavy deps.
- Handlers/MCP never touch SQL or Docker directly — go through `internal/core`.
- Store access only via `store.Store`; keep SQL portable (SQLite now, Postgres later). STRICT tables, TEXT ULIDs, one goose folder per dialect.
- Docker SDK is `github.com/moby/moby/client` (Options/Result struct API, not the old `docker/docker/client` signatures). Use `cerrdefs.IsNotFound`.
- Everything created on Docker carries `kipitiny.*` labels (`internal/docker`).
- Postgres services are always 1 replica.

## Commands
`make dev-api` / `make dev-ui` / `make test` / `make lint` / `make build` / `make docker`.
`go:embed` needs `web/dist`; make targets stub it via `ui-stub`.
