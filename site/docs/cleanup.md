---
title: Cleanup
description: Free disk space on every server, on a schedule or by hand
order: 12
---

*System › Cleanup* frees disk space on every server, on a cron schedule
(off by default) or with **Run now**. Each part is optional:

- **Images**: dangling layers only, or every image no container uses.
- **Volumes**: unused anonymous volumes, or unused named ones too (careful:
  that includes stopped stacks on the host that kipitiny doesn't manage).
- **Build cache**, **stopped containers** and **unused networks**
  that kipitiny didn't create.
- **Deployment history**: keep the last N deployments (and their logs)
  per service.

Nothing younger than the minimum age (24 h by default) is touched. Whatever the
settings, kipitiny keeps database and app volumes, everything it labels
`kipitiny.managed`, each service's image and those of its last successful
deployments (rollback), and the current and running deployments. Health probe
volumes left by older manager versions are removed once unused.
