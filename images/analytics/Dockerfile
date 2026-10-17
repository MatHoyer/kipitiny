# syntax=docker/dockerfile:1

# The analytics server (cmd/kipitiny-analytics), published with the manager as
# ghcr.io/mathoyer/kipitiny-analytics:<version>. Built from the repository
# root: it shares the module with the manager. No UI, no Node.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/kipitiny-analytics cmd/kipitiny-analytics
COPY internal internal
ARG VERSION=dev
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/kipitiny-analytics ./cmd/kipitiny-analytics

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/kipitiny-analytics /kipitiny-analytics
LABEL org.opencontainers.image.source=https://github.com/MatHoyer/kipitiny \
      org.opencontainers.image.description="kipitiny analytics server"
ENV KIPITINY_ANALYTICS_DATA_DIR=/data
# The manager mounts its volume here; the image creates it so a new volume
# starts owned by nonroot.
COPY --from=build --chown=65532:65532 /tmp /data
VOLUME /data
EXPOSE 8080 8081
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/kipitiny-analytics", "healthcheck"]
ENTRYPOINT ["/kipitiny-analytics"]
