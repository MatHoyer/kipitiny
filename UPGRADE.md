# Upgrade notices

What a release does to running servers that you should know before
upgrading: a short outage, a manual step. Managers show the notices of every
version between theirs and the new one next to the update, in the update
notification and on the GitHub release.

Add a notice under `## Unreleased` in the change that causes it, as
`- warning: …` (disruptive) or `- info: …`; continue on indented lines. The
release renames the section to its version.

## Unreleased
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
