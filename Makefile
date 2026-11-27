BIN := bin/kipitiny
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
# Static binary everywhere: app containers run it as their health probe.
export CGO_ENABLED := 0
PLAYWRIGHT_VERSION := $(shell node -p "require('./web/package.json').devDependencies['@playwright/test']" 2>/dev/null)
GOLANGCI_LINT_VERSION := v2.15.0
DEV_ENV := KIPITINY_ADDR=:8080 KIPITINY_DATA_DIR=./data KIPITINY_LOG_LEVEL=debug

.PHONY: all build ui ui-stub dev-api dev-ui dev-site site site-demo test cover e2e lint docker clean

all: build

ui:
	cd web && pnpm install --frozen-lockfile && pnpm build

# go:embed needs web/dist to exist; stub it when the UI hasn't been built.
ui-stub:
	@test -f web/dist/index.html || (mkdir -p web/dist && \
		echo '<!doctype html><p>UI not built. Run <code>make ui</code> or use the Vite dev server.</p>' > web/dist/index.html)

build: ui
	go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o $(BIN) ./cmd/kipitiny

# Go API on :8080; run `make dev-ui` alongside for the Vite dev server (proxies /api).
dev-api: ui-stub
	$(DEV_ENV) go run ./cmd/kipitiny

dev-ui:
	cd web && pnpm dev

# The website (landing page and docs), on :5173 like the UI: run one at a time.
dev-site: site-demo
	site/scripts/versions.sh
	cd site && pnpm install --frozen-lockfile && pnpm dev

# Static site in site/build/client, with the docs of each release since 0.10.
site: site-demo
	site/scripts/versions.sh
	cd site && pnpm install --frozen-lockfile && pnpm typecheck && pnpm build

# The manager's UI in demo mode, which the site serves at /demo.
site-demo:
	cd web && pnpm install --frozen-lockfile && pnpm build:demo
	rm -rf site/public/demo && cp -r web/dist-demo site/public/demo

# Every Go test, the Docker-backed ones included (they need a daemon), with the race
# detector and coverage of internal/; fails under COVERAGE_FLOOR, which only goes up.
COVERAGE_FLOOR := 68
cover: ui-stub
	CGO_ENABLED=1 KIPITINY_TEST_DOCKER=1 go test -race -timeout 30m -coverpkg=./internal/... -coverprofile=cover.out ./...
	@go tool cover -func=cover.out | awk -v floor=$(COVERAGE_FLOOR) '/^total:/ { sub("%", "", $$3); print "coverage " $$3 "% (floor " floor "%)"; if ($$3 + 0 < floor) exit 1 }'

test: ui-stub
	go test ./...
	cd web && node --test 'src/**/*.test.ts'

# UI tests: Playwright in its Docker image (no browser to install) against bin/kipitiny, which deploys on the daemon
# of DOCKER_HOST, else the local socket. Its reconciler removes kipitiny containers it doesn't know, so outside CI the
# daemon must have none (the Playwright container itself runs on the local daemon): give it a throwaway one (docker run -d --privileged -p 127.0.0.1:2375:2375 docker:dind
# --tls=false, then DOCKER_HOST=tcp://127.0.0.1:2375). ARGS= passes options, e.g. ARGS="--project=iphone deploy".
# The browsers get a network namespace of their own, like a user's on another machine: Chromium aborts the requests in
# flight (ERR_NETWORK_CHANGED) when the daemon adds a bridge next to it, as creating a project does. Host networking
# only to reach a DOCKER_HOST on the loopback, a dind whose bridges stay in its own namespace.
e2e: build
	@test -n "$$CI" || test -z "$$(docker ps -aq --filter label=kipitiny.managed)" || \
		(echo "make e2e: this Docker daemon runs kipitiny containers the tests would remove; set DOCKER_HOST to a throwaway one" && exit 1)
	env -u DOCKER_HOST docker run --rm $(if $(DOCKER_HOST),--network host) --ipc host -u $$(id -u):$$(id -g) -e HOME=/tmp -e CI \
		$(if $(DOCKER_HOST),-e DOCKER_HOST=$(DOCKER_HOST),-v /var/run/docker.sock:/var/run/docker.sock --group-add $$(stat -c %g /var/run/docker.sock)) \
		-v $(CURDIR):$(CURDIR) -w $(CURDIR)/web mcr.microsoft.com/playwright:v$(PLAYWRIGHT_VERSION)-noble \
		node_modules/.bin/playwright test $(ARGS)

lint: ui-stub
	@unformatted=$$(gofmt -l .); if [ -n "$$unformatted" ]; then echo "Run gofmt on:"; echo "$$unformatted"; exit 1; fi
	go vet ./...
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run
	cd web && pnpm typecheck && pnpm lint
	cd site && pnpm typecheck

docker:
	docker build --build-arg VERSION=$(VERSION) -t kipitiny -t ghcr.io/kipit-labs/kipitiny:dev .

clean:
	rm -rf bin web/dist
