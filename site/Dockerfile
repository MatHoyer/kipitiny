# The build runs on the runner's platform: its output is static files, the
# same for every architecture, and node under QEMU emulation is slow and can
# crash (SIGILL).
FROM --platform=$BUILDPLATFORM node:24-alpine AS build
WORKDIR /src
RUN corepack enable
COPY package.json pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY . .
# The release this site documents, shown in the footer.
ARG KIPITINY_VERSION
ENV KIPITINY_VERSION=$KIPITINY_VERSION
RUN pnpm typecheck && pnpm build

FROM nginx:1-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/build/client /usr/share/nginx/html
EXPOSE 80
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://127.0.0.1/ >/dev/null || exit 1
