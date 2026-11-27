# kipitiny

Design doc: `docs/design.md` — read before architectural changes. Roadmap in §16.

## Rules
- Manager is ONE process: no Postgres/Redis/worker containers for its own state.
- RAM budget matters: no Node at runtime, no heavy deps.
- Handlers/MCP never touch SQL or Docker directly — go through `internal/core`.
- `make lint` runs golangci-lint (`.golangci.yml`: depguard layer rules, files under 500 lines) on the code changed
  since `issues.new-from-rev`, and oxlint (`web/.oxlintrc.json`, `max-lines` 500) on the UI. Never `//nolint`: fix the
  dependency or split the file. The move to the layered layout goes area by area (#522).
- Store access only via `store.Store`; keep SQL portable (SQLite now, Postgres later). STRICT tables, TEXT ULIDs, one goose folder per dialect.
- Docker SDK is `github.com/moby/moby/client` (Options/Result struct API, not the old `docker/docker/client` signatures). Use `cerrdefs.IsNotFound`.
- Everything created on Docker carries `kipitiny.*` labels (`internal/docker`).
- Postgres services are always 1 replica.
- UI primitives (`@kipit-labs/kipitidy/*`: buttons, dialogs, form fields, theme tokens) live in the kipitidy repo
  (`kipit-labs/kipitidy`): change them there, release, then bump the version in `web/`. App components stay in `web/src/components`.
- User docs live in `site/docs/*.md` (the website), not in the manager; the MCP instructions link to them.
- A change that disrupts running servers on upgrade (Traefik recreated, a manual step) adds a notice under `## Unreleased` in `UPGRADE.md`; managers show it before updating.

## Testing
The tests are the safety net that lets agents change code: a change is done when `make cover`, `make e2e` and
`make lint` pass, run, not assumed. Each layer is required for what it covers:

| Layer | Where | Covers |
|---|---|---|
| Unit | `*_test.go` next to the code, store on a temp SQLite | business rules, edge cases |
| Docker integration | `internal/core/*_docker_test.go` (`KIPITINY_TEST_DOCKER=1`), `flows_docker_test.go` for deploy, rollback, reconcile, compose, backups, previews | what core does on a real daemon |
| HTTP and MCP contract | `internal/api/*_test.go`, `routeAccess` (`access.go`) checked over HTTP by `TestRouteAccessOverHTTP`; `internal/mcp` `toolAccess` by `TestToolAccess` | status codes, who may call each route and tool |
| UI end to end | `web/e2e/*.e2e.ts` (Playwright): the real binary deploying on a Docker daemon, WebKit at iPhone size and desktop Chromium, outside requests answered locally | user journeys in a browser, phone layout, uncaught page errors |

- A new route goes into `routeAccess`, a new MCP tool into `toolAccess`; a new user-facing flow adds or extends a UI
  journey.
- Tests that deploy through core (`flows_docker_test.go`, `make e2e`) need a Docker daemon of their own: the
  reconciler removes `kipitiny.managed` containers it doesn't know. Locally, a throwaway one
  (`docker run -d --privileged -p 127.0.0.1:2375:2375 docker:dind --tls=false`, then `DOCKER_HOST=tcp://127.0.0.1:2375`);
  outside CI, the flow tests skip and `make e2e` stops on a daemon that runs kipitiny containers.
- UI tests find elements by role and label, as users and screen readers do, never by CSS class or test id: an element
  a test cannot find by name is an accessibility bug. They wait on state or responses, never on timeouts. Any
  uncaught page error fails the test (`e2e/fixtures.ts`). Each test joins with its own account and names its projects
  with `account.project(...)`, deleted with their containers after the test.
- Write the test first and watch it fail for the right reason: a test that never failed proves nothing. For a bug,
  the failing test is the reproduction.
- Never weaken, skip or delete a test, or lower `COVERAGE_FLOOR` to get green. If a test is wrong, fix it in its own
  commit and say why in the PR.
- A refactor keeps the contract and UI tests unchanged: editing one means behavior changed, and the PR says so.
- Tests are deterministic: no sleeps, no outside network except image pulls, and they clean up what they create.
- `COVERAGE_FLOOR` only goes up: raise it when coverage does.
- A new UI test runs repeated before it is trusted: `make e2e ARGS="--repeat-each 5"`. A flaky test is a bug, never
  retried away (`retries: 0`).

## Commands
`make dev-api` / `make dev-ui` / `make dev-site` / `make test` / `make cover` / `make e2e` / `make lint` / `make build` / `make docker`.
`make e2e` runs Playwright in its Docker image (no browser to install); `ARGS=` passes options, e.g.
`ARGS="--project=iphone backup"`.
`go:embed` needs `web/dist`; make targets stub it via `ui-stub`.
