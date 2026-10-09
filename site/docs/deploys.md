---
title: Deploys
description: Blue-green deploys, readiness, rollbacks, volumes, access rules and deploying from CI
order: 4
---

Apps deploy blue-green: every new replica starts next to the old ones, and old
replicas are only stopped once *all* new ones are ready. If one crashes or never
gets ready, the new containers are removed and the old version keeps serving.

- **Ready** means the image's own `HEALTHCHECK` passes, or else an injected probe
  (the manager's static binary, mounted read-only) sees the port accept
  connections or the *health path* answer `< 400`. Traefik only routes to healthy
  containers, so traffic never reaches a replica that isn't serving. Services
  without a port just need to stay up for 5 s.
- A **pre-deploy command** (e.g. migrations) runs once with `sh -c` in a one-off
  container before any replica starts; a failure aborts the deploy.
- Old replicas are stopped one by one and kept (stopped) until the next deploy so
  their logs stay readable. **Roll back** redeploys an earlier image.
- Databases are recreated in place (a volume can't be shared by two servers).
- Apps with **published ports** can't run two versions side by side (the port is
  taken), so the old replica stops before the new one starts: a short downtime.
  If the new one doesn't get ready, it is removed and the old one starts again.
  The probe checks the first published TCP port when the app has no HTTP port.
- A **stop grace period** (default 10 s, up to 600) is how long an app gets to
  exit after `SIGTERM` before it is killed, e.g. for a game server saving its
  world. It applies to deploys, stops and Docker's own restarts.
- **Volumes** keep an app's files across deploys (e.g. uploads): each has a
  name and a mount path, and every replica mounts the same volume. Deleting the
  service destroys them, so it asks for the service name.
- **Access** settings put Traefik middlewares on an app's domain: basic auth
  (passwords stored bcrypt-hashed, or password manager references fetched at
  each deploy), an IP allowlist, a per-IP rate limit and custom response headers
  (e.g. `X-Robots-Tag: noindex` for staging). They apply from the next deploy.

A **reconciler** keeps Docker matching the store every 30 s, shortly after any
change, and whenever a managed container dies or is removed: missing replicas
are recreated from the deployed version, externally stopped ones restarted,
extra ones removed (so changing the replica count applies without a deploy),
and containers of deleted services cleaned up. A service stopped from the UI
stays stopped. Services busy with a deploy, backup or restore are left alone.

## Deploy from CI

Services run Docker images; kipitiny doesn't build them. Build, test and push
the image once in CI, then have kipitiny deploy that exact image.
Create a **deploy** token (Settings), keep it as a CI secret, and end the
pipeline with `kipitiny deploy` (in the manager image):

```yaml
# .github/workflows/deploy.yml (after docker/build-push-action pushed
# ghcr.io/org/shop:sha-${{ github.sha }})
- name: Deploy
  run: >
    docker run --rm -e KIPITINY_TOKEN=${{ secrets.KIPITINY_TOKEN }}
    ghcr.io/mathoyer/kipitiny deploy
    --url https://kipitiny.example.com --service shop/web
    --tag sha-${{ github.sha }} --commit ${{ github.sha }}
```

It starts the deployment, prints its log and exits non-zero if it fails
(`--timeout`, default 20m; `--no-wait` to return at once). `--tag` (or
`--digest sha256:…`) replaces only the tag of the service's image, never its
repository, so a deploy token can't run another image; once deployed, the
service keeps it. The same is `POST /api/services/<id>/deploy` with
`{"tag": "…", "commit": "…"}`, or the MCP `deploy` tool's `tag`.

Pulled images are pinned to their digest (`name:tag@sha256:…`): replicas the
reconciler recreates and rollbacks run the image that was deployed, even if the
tag moved since. Each deployment records who triggered it (user or token)
and, when given, the commit.
