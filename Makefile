BIN := bin/kipitiny
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
# Static binary everywhere: app containers run it as their health probe.
export CGO_ENABLED := 0
DEV_ENV := KIPITINY_ADDR=:8080 KIPITINY_DATA_DIR=./data KIPITINY_LOG_LEVEL=debug

.PHONY: all build ui ui-stub dev-api dev-ui test lint docker clean

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

test: ui-stub
	go test ./...

lint: ui-stub
	go vet ./...
	cd web && pnpm typecheck

docker:
	docker build --build-arg VERSION=$(VERSION) -t kipitiny .

clean:
	rm -rf bin web/dist
