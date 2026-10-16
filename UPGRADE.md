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
- warning: `restart` and `depends_on` in compose files now apply. A service
  with `restart: "no"` becomes a job: its deploys wait for it to exit 0, so a
  long-running server written that way fails to deploy; drop the line (or use
  `restart: unless-stopped`). With `on-failure`, a container that exits isn't
  started again by kipitiny. `depends_on` naming a service missing from the
  file, or a cycle, is now an error.
- warning: `deploy.restart_policy` now applies like `restart` (`condition:
  none` makes the service a job, see above).
- info: A database's `environment` in compose files is no longer all
  ignored: a new database takes its user, database name and password from
  `POSTGRES_USER`, `POSTGRES_DB`, `POSTGRES_PASSWORD` and the like. Existing
  databases keep theirs (a sync reports the difference). Exports now write
  the user and database name.
- warning: `networks` in compose files now apply. A service listing
  networks is only on those: it no longer reaches the project's services on
  other networks (add `default` to keep the project network), and gets
  exactly the networks created by hand it lists as `external: true` (others
  set in the UI are detached). An external network must exist on the
  server, or the sync fails.

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
