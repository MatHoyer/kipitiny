# Upgrade notices

What a release does to running servers that you should know before
upgrading: a short outage, a manual step. Managers show the notices of every
version between theirs and the new one next to the update, in the update
notification and on the GitHub release.

Add a notice under `## Unreleased` in the change that causes it, as
`- warning: …` (disruptive) or `- info: …`; continue on indented lines. The
release renames the section to its version.

## Unreleased
- warning: Compose keys kipitiny used to drop now apply, so the next git sync
  (or apply) of a file using them updates and redeploys those services: a
  port's host IP (`127.0.0.1:5432:5432` was bound on every address, now only
  on 127.0.0.1), `:ro` on volumes, `labels`, `init`, `extra_hosts`,
  `stop_signal`, and top-level `external` volumes (the app then mounts that
  volume instead of the one kipitiny created: its data there is no longer
  seen) and `driver_opts`. External volumes and driver options need an admin:
  a git project linked by a member fails to sync until they're removed.

## 0.33.0
- warning: API tokens lose their scope (read, deploy, admin): a token does what
  its owner may, or only what it's restricted to. Read and deploy tokens
  become restricted tokens on the projects their owner could reach at the
  upgrade (read: view and data; deploy: plus deploy and backups); they don't
  reach projects created later, nor users and settings. Create a new token if
  one needs more.

## 0.29.0
- warning: Sleep mode moves from apps to projects: a sleeping project stops
  every service, databases and workers too. Apps' own sleep settings are
  dropped, and apps asleep during the upgrade start again; turn sleep on
  again in the project's settings. `x-kipitiny.sleep_after` in compose files
  is ignored with a warning. Previews keep sleeping, as a whole now.
- warning: The maintenance page and mode move from apps to projects. Each
  project keeps the page of its first app that had one, and maintenance mode
  is off after the upgrade: apps that were in maintenance mode are reachable
  again. Upgrade outside a maintenance window, or turn it back on in the
  project's settings right after.

## 0.28.0
- warning: Traefik restarts once on every server (it gets a metrics
  entrypoint for sleep mode): apps are unreachable for a few seconds. Upgrade
  at a quiet time.
