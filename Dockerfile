# syntax=docker/dockerfile:1

# UI and binary build on the runner's platform; Go cross-compiles for the target.
FROM --platform=$BUILDPLATFORM node:24-alpine AS ui
RUN corepack enable
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /src/web/dist ./web/dist
ARG VERSION=dev
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/kipitiny ./cmd/kipitiny

# Runs as root: it needs the host Docker socket, which is root-equivalent anyway.
FROM gcr.io/distroless/static-debian12
COPY --from=build /out/kipitiny /kipitiny
ENV KIPITINY_DATA_DIR=/data
VOLUME /data
EXPOSE 3000
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD ["/kipitiny", "healthcheck"]
ENTRYPOINT ["/kipitiny"]
