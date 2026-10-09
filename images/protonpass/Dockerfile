# syntax=docker/dockerfile:1

# Proton Pass CLI for pass:// env references, published as
# ghcr.io/mathoyer/kipitiny-protonpass:<PASS_CLI_VERSION>. The manager runs it
# on demand through the Docker socket, so pass-cli stays out of its own image.
# Pinned and checksummed from https://proton.me/download/pass-cli/versions.json.
FROM --platform=$BUILDPLATFORM alpine:3.22 AS download
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

# pass-cli needs glibc. The session lives in /session, a volume the manager
# mounts; the image creates it so a new volume starts owned by the user.
FROM debian:12-slim
ARG PASS_CLI_VERSION=2.4.2
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/* && \
    useradd --uid 10001 --home-dir /session --no-create-home pass && \
    install -d -o pass -g pass -m 700 /session
COPY --from=download /pass-cli /usr/local/bin/pass-cli
LABEL org.opencontainers.image.source=https://github.com/MatHoyer/kipitiny \
      org.opencontainers.image.description="Proton Pass CLI for kipitiny" \
      org.opencontainers.image.version=$PASS_CLI_VERSION
ENV HOME=/session \
    PROTON_PASS_SESSION_DIR=/session \
    PROTON_PASS_KEY_PROVIDER=fs \
    PROTON_PASS_NO_UPDATE_CHECK=1 \
    PASS_LOG_LEVEL=off \
    NO_COLOR=1
USER pass
WORKDIR /session
ENTRYPOINT ["pass-cli"]
