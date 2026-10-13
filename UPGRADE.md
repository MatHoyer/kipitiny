# Upgrade notices

What a release does to running servers that you should know before
upgrading: a short outage, a manual step. Managers show the notices of every
version between theirs and the new one next to the update, in the update
notification and on the GitHub release.

Add a notice under `## Unreleased` in the change that causes it, as
`- warning: …` (disruptive) or `- info: …`; continue on indented lines. The
release renames the section to its version.

## 0.28.0
- warning: Traefik restarts once on every server (it gets a metrics
  entrypoint for sleep mode): apps are unreachable for a few seconds. Upgrade
  at a quiet time.
