---
title: Upgrading
description: Update the manager from the UI or the shell, without stopping apps
order: 2
---

The sidebar shows **Update available** when a newer version is published
(checked every 6 hours); **Update now** checks first that it's a good time,
then installs it:

- **Nothing in progress**: no deploy, backup, restore, restore test or cleanup
  running, since the restart would cut it short. The button stays off until
  they end, and the check refreshes on its own.
- **Docker** answers.
- **A recent manager backup** (within a day): only a warning, with a link to
  back it up first.

Or from the shell:

```sh
docker compose pull && docker compose up -d
```

Either way only the manager restarts: apps, databases and Traefik keep running,
unless the release says otherwise.
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

## Before upgrading

Some releases do something to running servers you'd rather plan for: Traefik
restarted (apps unreachable for a few seconds), a manual step. They say so
in [UPGRADE.md](https://github.com/MatHoyer/kipitiny/blob/main/UPGRADE.md),
and you see it:

- in the sidebar's **Update available**, and in the update dialog, which
  waits for you to confirm the time is right before **Update now**;
- in the "update available" [notification](/docs/monitoring#notifications), sent as a
  warning;
- at the top of the release's notes on GitHub.

Skipping versions shows the notices of every version in between. Updating
from the shell skips the dialog: read the release notes first.
