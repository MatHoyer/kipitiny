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

# Proton Pass CLI, for pass:// env references. Pinned and checksummed from
# https://proton.me/download/pass-cli/versions.json.
FROM --platform=$BUILDPLATFORM alpine:3.22 AS passcli
ARG TARGETARCH
ARG PASS_CLI_VERSION=2.4.2
ARG PASS_CLI_SHA256_AMD64=4089bdf5981140ac5bee65d2d79bf98767537f54d33197b79e5f86c630714842
ARG PASS_CLI_SHA256_ARM64=eee38fc4549a5dfcbb794aafcb6d517c9e0cd0dc3c53bbb2997fd8142ed681c7
RUN case "$TARGETARCH" in \
      amd64) arch=x86_64 sum=$PASS_CLI_SHA256_AMD64 ;; \
      arm64) arch=aarch64 sum=$PASS_CLI_SHA256_ARM64 ;; \
      *) echo "no pass-cli for $TARGETARCH" >&2; exit 1 ;; \
    esac && \
    wget -qO /pass-cli "https://proton.me/download/pass-cli/$PASS_CLI_VERSION/pass-cli-linux-$arch" && \
    echo "$sum  /pass-cli" | sha256sum -c - && \
    chmod 755 /pass-cli

# rclone, for Google Drive and Proton Drive backup targets. Pinned and
# checksummed from https://downloads.rclone.org/<version>/SHA256SUMS.
FROM --platform=$BUILDPLATFORM alpine:3.22 AS rclone
ARG TARGETARCH
ARG RCLONE_VERSION=1.75.1
ARG RCLONE_SHA256_AMD64=982b5aa772841168f8e380f139e9e787b2a105403e32b94da8676a0e1c0a13ab
ARG RCLONE_SHA256_ARM64=03f2504174034b6d004152ed7369251c9a9ec1f7e0836eda420f5c7a5ec0dff9
RUN case "$TARGETARCH" in \
      amd64) sum=$RCLONE_SHA256_AMD64 ;; \
      arm64) sum=$RCLONE_SHA256_ARM64 ;; \
      *) echo "no rclone for $TARGETARCH" >&2; exit 1 ;; \
    esac && \
    wget -qO /rclone.zip "https://downloads.rclone.org/v$RCLONE_VERSION/rclone-v$RCLONE_VERSION-linux-$TARGETARCH.zip" && \
    echo "$sum  /rclone.zip" | sha256sum -c - && \
    unzip -q /rclone.zip -d /tmp && \
    mv /tmp/rclone-*/rclone /rclone && \
    chmod 755 /rclone

# Runs as root: it needs the host Docker socket, which is root-equivalent anyway.
# The cc variant has glibc for pass-cli; kipitiny itself stays static.
FROM gcr.io/distroless/cc-debian12
COPY --from=build /out/kipitiny /kipitiny
COPY --from=passcli /pass-cli /usr/local/bin/pass-cli
COPY --from=rclone /rclone /usr/local/bin/rclone
ENV KIPITINY_DATA_DIR=/data
VOLUME /data
EXPOSE 3000
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD ["/kipitiny", "healthcheck"]
ENTRYPOINT ["/kipitiny"]
