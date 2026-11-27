BIN := bin/kipitiny
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
# Static binary everywhere: app containers run it as their health probe.
export CGO_ENABLED := 0
DEV_ENV := KIPITINY_ADDR=:8080 KIPITINY_DATA_DIR=./data KIPITINY_LOG_LEVEL=debug

.PHONY: all build ui ui-stub dev-api dev-ui dev-site site site-demo test cover lint docker clean

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
COVERAGE_FLOOR := 66
cover: ui-stub
	CGO_ENABLED=1 KIPITINY_TEST_DOCKER=1 go test -race -timeout 30m -coverpkg=./internal/... -coverprofile=cover.out ./...
	@go tool cover -func=cover.out | awk -v floor=$(COVERAGE_FLOOR) '/^total:/ { sub("%", "", $$3); print "coverage " $$3 "% (floor " floor "%)"; if ($$3 + 0 < floor) exit 1 }'

test: ui-stub
	go test ./...
	cd web && node --test 'src/**/*.test.ts'

lint: ui-stub
	@unformatted=$$(gofmt -l .); if [ -n "$$unformatted" ]; then echo "Run gofmt on:"; echo "$$unformatted"; exit 1; fi
	go vet ./...
	cd web && pnpm typecheck
	cd site && pnpm typecheck

docker:
	docker build --build-arg VERSION=$(VERSION) -t kipitiny -t ghcr.io/kipit-labs/kipitiny:dev .

clean:
	rm -rf bin web/dist
