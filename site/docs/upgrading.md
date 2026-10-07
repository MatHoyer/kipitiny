---
title: Upgrading
description: Update the manager from the UI or the shell, without stopping apps
order: 2
---

The sidebar shows **Update available** when a newer version is published
(checked every 6 hours); **Update now** installs it. Or from the shell:

```sh
docker compose pull && docker compose up -d
```

Either way only the manager restarts: apps, databases and Traefik keep running.
Running deploys and backups finish first (up to 5 minutes), and before database
migrations the manager keeps a copy of its state as
`/data/kipitiny.db.pre-migrate-<version>` (last 3 kept).

The UI update pulls the new image and starts a short-lived `kipitiny-updater`
container that swaps the manager's container, keeping its configuration,
volumes and networks. If the new version isn't healthy within 3 minutes, the
previous one is restored; the updater's output ends up in the manager's log.
It needs the manager to run in Docker. With a compose file pinning
`KIPITINY_VERSION`, the button is off (the next `up` would go back to that
version): change the variable and run `docker compose up -d` instead.
System › About checks for a new release on demand.
