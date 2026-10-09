---
title: Backups
description: Database and volume backups, restore tests, encryption, retention and storage targets
order: 7
---

- `pg_dump -Fc` runs **inside** the database container (client always matches the
  server) and streams straight to the target: local disk (`/data/backups`) or
  any S3-compatible bucket. Memory stays constant and nothing touches a temp
  file.
- A backup only counts once `pg_dump` exits 0; partial uploads are deleted. Each
  backup records size, SHA-256, server version and duration.
- Restores load the dump into a scratch database and swap it in by rename, so the
  result is exactly the backup and a failed restore leaves live data untouched.
  Apps referencing the database are stopped meanwhile. The checksum is verified.
- **Volumes** of Redis services and of apps are backed up as a gzipped tar (one
  folder per volume, owners and permissions kept), read by a short-lived
  network-less `busybox` container that mounts them read-only; the service keeps
  running, so set a *pre-backup command* on an app to flush its files first.
  Restores extract into a staging volume and check the checksum before touching
  anything, then stop the service, replace each volume's content and start it
  again.
- Backups outlive their service and project; delete them explicitly.
- **Schedules** (cron, UTC unless `CRON_TZ=` is given) back up a service to a
  target and then apply retention to *their own* backups: keep the last N, plus
  the newest of each of the last N days / ISO weeks / months. Manual backups are
  never pruned. A run that finds the service busy (deploy, restore) retries for
  15 minutes.

## Restore tests

A backup nobody restored is a hope. *Verify* (or a schedule with restore tests on,
the default) restores a backup into a throwaway PostgreSQL container with no
network, matching the backup's major version, then runs `ANALYZE` and records
tables, estimated rows, database size and duration on the backup. The container
and its volume are always removed; one test runs at a time. A volume backup's
test reads the whole archive back (decrypt, gunzip, list) in a throwaway
container, checks the checksum and records the number of entries.

## Encryption

An S3 target can encrypt everything stored on it with [age](https://age-encryption.org)
(chosen at creation; the key never changes). Copy the key (*Show key*) somewhere
safe: without it, those backups are unreadable if this server is lost. Offline:

```sh
age -d -i key.txt shop-20260930T030000Z-xxxx.dump.age | pg_restore -d "$DATABASE_URL" --no-owner
age -d -i key.txt web-20260930T030000Z-xxxx.tar.gz.age | tar -xzf - --numeric-owner   # volumes
```

## Manager state

The manager's own SQLite file (projects, services, schedules, credentials, keys)
is snapshotted with `VACUUM INTO` on schedules you manage in Backups › Manager
state, to any storage, with the same retention rules as databases. On first
start a daily schedule to local disk keeping the last 14 is created
(`KIPITINY_MANAGER_BACKUP_CRON` / `_TARGET` / `_KEEP`, `off` to skip it). These
snapshots contain every secret the manager holds: prefer an encrypted storage.
To restore one, stop the manager and replace `/data/kipitiny.db` with the file
(delete `kipitiny.db-wal` and `kipitiny.db-shm` first).

## Targets

S3-compatible targets take a bucket, an optional prefix and credentials.
Cloudflare R2, AWS S3, Backblaze B2 and Hetzner Object Storage have their own
form, which builds the endpoint from the account ID and jurisdiction (R2:
default, EU or FedRAMP, as chosen when the bucket was created), the region (AWS,
B2) or the location (Hetzner). Any other provider (MinIO, Garage, Scaleway,
OVH…) takes the endpoint, region and HTTPS setting directly. Targets are checked
(a test object is written and deleted) before they are saved.
