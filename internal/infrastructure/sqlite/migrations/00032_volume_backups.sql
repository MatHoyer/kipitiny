-- Volume backups: an archive of a service's volumes (redis, apps with
-- volumes). SQLite can't change a CHECK constraint, so backups and
-- backup_schedules are rebuilt (with foreign keys off: restores reference
-- backups).

-- +goose NO TRANSACTION
-- +goose Up
PRAGMA foreign_keys = OFF;
BEGIN;
CREATE TABLE backups_new (
    id             TEXT PRIMARY KEY,
    kind           TEXT NOT NULL DEFAULT 'postgres' CHECK (kind IN ('postgres', 'manager', 'volume')),
    service_id     TEXT NOT NULL,
    project_id     TEXT NOT NULL,
    service_name   TEXT NOT NULL,
    project_name   TEXT NOT NULL,
    target_id      TEXT NOT NULL REFERENCES backup_targets (id),
    schedule_id    TEXT NOT NULL DEFAULT '',
    object_key     TEXT NOT NULL,
    status         TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
    size_bytes     INTEGER NOT NULL DEFAULT 0,
    sha256         TEXT NOT NULL DEFAULT '',
    encrypted      INTEGER NOT NULL DEFAULT 0,
    pg_version     TEXT NOT NULL DEFAULT '',
    volumes        TEXT NOT NULL DEFAULT '[]',
    duration_ms    INTEGER NOT NULL DEFAULT 0,
    error          TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    finished_at    TEXT,
    verify_status  TEXT NOT NULL DEFAULT '' CHECK (verify_status IN ('', 'running', 'succeeded', 'failed')),
    verify_error   TEXT NOT NULL DEFAULT '',
    verify_details TEXT NOT NULL DEFAULT '{}',
    verified_at    TEXT
) STRICT;
INSERT INTO backups_new (id, kind, service_id, project_id, service_name, project_name, target_id, schedule_id, object_key, status,
    size_bytes, sha256, encrypted, pg_version, duration_ms, error, created_at, finished_at, verify_status, verify_error, verify_details, verified_at)
SELECT id, kind, service_id, project_id, service_name, project_name, target_id, schedule_id, object_key, status,
    size_bytes, sha256, encrypted, pg_version, duration_ms, error, created_at, finished_at, verify_status, verify_error, verify_details, verified_at
FROM backups;
DROP TABLE backups;
ALTER TABLE backups_new RENAME TO backups;
CREATE INDEX backups_service_id ON backups (service_id, created_at);

CREATE TABLE backup_schedules_new (
    id           TEXT PRIMARY KEY,
    kind         TEXT NOT NULL DEFAULT 'postgres' CHECK (kind IN ('postgres', 'manager', 'volume')),
    service_id   TEXT REFERENCES services (id) ON DELETE CASCADE,
    target_id    TEXT NOT NULL REFERENCES backup_targets (id),
    cron         TEXT NOT NULL,
    keep_last    INTEGER NOT NULL DEFAULT 0,
    keep_daily   INTEGER NOT NULL DEFAULT 0,
    keep_weekly  INTEGER NOT NULL DEFAULT 0,
    keep_monthly INTEGER NOT NULL DEFAULT 0,
    enabled      INTEGER NOT NULL DEFAULT 1,
    created_at   TEXT NOT NULL,
    verify       INTEGER NOT NULL DEFAULT 1,
    CHECK ((kind = 'manager') = (service_id IS NULL))
) STRICT;
INSERT INTO backup_schedules_new (id, kind, service_id, target_id, cron, keep_last, keep_daily, keep_weekly, keep_monthly, enabled, created_at, verify)
SELECT id, kind, service_id, target_id, cron, keep_last, keep_daily, keep_weekly, keep_monthly, enabled, created_at, verify
FROM backup_schedules;
DROP TABLE backup_schedules;
ALTER TABLE backup_schedules_new RENAME TO backup_schedules;
CREATE INDEX backup_schedules_service_id ON backup_schedules (service_id);
COMMIT;
PRAGMA foreign_keys = ON;

-- A command run in the app before each volume backup (e.g. flush to disk).
ALTER TABLE services ADD COLUMN pre_backup TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE services DROP COLUMN pre_backup;
-- Foreign keys still on: their restores go with them.
DELETE FROM backups WHERE kind = 'volume';
DELETE FROM backup_schedules WHERE kind = 'volume';
PRAGMA foreign_keys = OFF;
BEGIN;
CREATE TABLE backups_old (
    id             TEXT PRIMARY KEY,
    service_id     TEXT NOT NULL,
    project_id     TEXT NOT NULL,
    service_name   TEXT NOT NULL,
    project_name   TEXT NOT NULL,
    target_id      TEXT NOT NULL REFERENCES backup_targets (id),
    object_key     TEXT NOT NULL,
    status         TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
    size_bytes     INTEGER NOT NULL DEFAULT 0,
    sha256         TEXT NOT NULL DEFAULT '',
    pg_version     TEXT NOT NULL DEFAULT '',
    duration_ms    INTEGER NOT NULL DEFAULT 0,
    error          TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    finished_at    TEXT,
    schedule_id    TEXT NOT NULL DEFAULT '',
    kind           TEXT NOT NULL DEFAULT 'postgres' CHECK (kind IN ('postgres', 'manager')),
    encrypted      INTEGER NOT NULL DEFAULT 0,
    verify_status  TEXT NOT NULL DEFAULT '' CHECK (verify_status IN ('', 'running', 'succeeded', 'failed')),
    verify_error   TEXT NOT NULL DEFAULT '',
    verify_details TEXT NOT NULL DEFAULT '{}',
    verified_at    TEXT
) STRICT;
INSERT INTO backups_old (id, service_id, project_id, service_name, project_name, target_id, object_key, status, size_bytes, sha256,
    pg_version, duration_ms, error, created_at, finished_at, schedule_id, kind, encrypted, verify_status, verify_error, verify_details, verified_at)
SELECT id, service_id, project_id, service_name, project_name, target_id, object_key, status, size_bytes, sha256,
    pg_version, duration_ms, error, created_at, finished_at, schedule_id, kind, encrypted, verify_status, verify_error, verify_details, verified_at
FROM backups;
DROP TABLE backups;
ALTER TABLE backups_old RENAME TO backups;
CREATE INDEX backups_service_id ON backups (service_id, created_at);

CREATE TABLE backup_schedules_old (
    id           TEXT PRIMARY KEY,
    kind         TEXT NOT NULL DEFAULT 'postgres' CHECK (kind IN ('postgres', 'manager')),
    service_id   TEXT REFERENCES services (id) ON DELETE CASCADE,
    target_id    TEXT NOT NULL REFERENCES backup_targets (id),
    cron         TEXT NOT NULL,
    keep_last    INTEGER NOT NULL DEFAULT 0,
    keep_daily   INTEGER NOT NULL DEFAULT 0,
    keep_weekly  INTEGER NOT NULL DEFAULT 0,
    keep_monthly INTEGER NOT NULL DEFAULT 0,
    enabled      INTEGER NOT NULL DEFAULT 1,
    created_at   TEXT NOT NULL,
    verify       INTEGER NOT NULL DEFAULT 1,
    CHECK ((kind = 'manager') = (service_id IS NULL))
) STRICT;
INSERT INTO backup_schedules_old (id, kind, service_id, target_id, cron, keep_last, keep_daily, keep_weekly, keep_monthly, enabled, created_at, verify)
SELECT id, kind, service_id, target_id, cron, keep_last, keep_daily, keep_weekly, keep_monthly, enabled, created_at, verify
FROM backup_schedules;
DROP TABLE backup_schedules;
ALTER TABLE backup_schedules_old RENAME TO backup_schedules;
CREATE INDEX backup_schedules_service_id ON backup_schedules (service_id);
COMMIT;
PRAGMA foreign_keys = ON;
