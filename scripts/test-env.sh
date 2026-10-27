#!/usr/bin/env bash
# A test manager per worktree, isolated from the others: its own Docker daemon
# (docker:dind), so no reconciler sees another manager's containers.
#
#   scripts/test-env.sh up        build this worktree as :dev-<branch>, (re)start its manager
#   scripts/test-env.sh url       the manager's URL, and Traefik's ports for *.localhost apps
#   scripts/test-env.sh down      delete the environment and its data
#   scripts/test-env.sh <cmd...>  run a command inside it, e.g. `docker logs kipitiny-manager-1`
set -euo pipefail
cd "$(dirname "$0")/.."

slug=$(git rev-parse --abbrev-ref HEAD | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9\n' '-' | cut -c1-40)
name="kipitiny-test-$slug"
image="ghcr.io/mathoyer/kipitiny:dev-$slug"

ports() {
  echo "manager: http://$(docker port "$name" 3000/tcp | head -1)"
  echo "traefik: http $(docker port "$name" 80/tcp | head -1), https $(docker port "$name" 443/tcp | head -1)"
}

case "${1:-}" in
up)
  docker build --build-arg VERSION="dev-$slug" -t "$image" .
  if ! docker inspect "$name" >/dev/null 2>&1; then
    # Random localhost ports: several environments run side by side.
    docker run -d --privileged --name "$name" --label kipitiny.test-env="$slug" \
      -v "$name:/var/lib/docker" -e DOCKER_TLS_CERTDIR= \
      -p 127.0.0.1::3000 -p 127.0.0.1::80 -p 127.0.0.1::443 docker:29-dind >/dev/null
  fi
  docker start "$name" >/dev/null
  until docker exec "$name" docker info >/dev/null 2>&1; do sleep 1; done
  docker save "$image" | docker exec -i "$name" docker load -q
  # The production compose file, so the manager sees the same labels and volume.
  docker exec -i -e KIPITINY_VERSION="dev-$slug" "$name" docker compose -f - up -d <docker-compose.yml
  ports
  ;;
url) ports ;;
down)
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker volume rm "$name" >/dev/null 2>&1 || true
  docker image rm "$image" >/dev/null 2>&1 || true
  ;;
"") sed -n '2,9p' "$0" | cut -c3- && exit 1 ;;
*) docker exec -i "$name" "$@" ;;
esac
