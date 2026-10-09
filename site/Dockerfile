# syntax=docker/dockerfile:1
# Built from the repository root: the site embeds the manager's UI in demo
# mode (/demo/), built from web/.
#
# The builds run on the runner's platform: their output is static files, the
# same for every architecture, and node under QEMU emulation is slow and can
# crash (SIGILL).
FROM --platform=$BUILDPLATFORM node:24-alpine AS demo
RUN corepack enable
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build:demo

FROM --platform=$BUILDPLATFORM node:24-alpine AS build
RUN corepack enable
WORKDIR /src
COPY site/package.json site/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY site/ ./
COPY --from=demo /src/web/dist-demo ./public/demo
# The release this site documents, shown in the footer.
ARG KIPITINY_VERSION
ENV KIPITINY_VERSION=$KIPITINY_VERSION
RUN pnpm typecheck && pnpm build

FROM nginx:1-alpine
COPY site/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/build/client /usr/share/nginx/html
EXPOSE 80
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://127.0.0.1/ >/dev/null || exit 1
