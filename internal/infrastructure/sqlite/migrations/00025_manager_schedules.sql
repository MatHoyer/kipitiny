-- Schedules can back up the manager itself (kind 'manager', no service).
-- SQLite can't drop NOT NULL, so the table is rebuilt.

-- +goose NO TRANSACTION
-- +goose Up
PRAGMA foreign_keys = OFF;
BEGIN;
CREATE TABLE backup_schedules_new (
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
INSERT INTO backup_schedules_new (id, service_id, target_id, cron, keep_last, keep_daily, keep_weekly, keep_monthly, enabled, created_at, verify)
SELECT id, service_id, target_id, cron, keep_last, keep_daily, keep_weekly, keep_monthly, enabled, created_at, verify
FROM backup_schedules;
DROP TABLE backup_schedules;
ALTER TABLE backup_schedules_new RENAME TO backup_schedules;
CREATE INDEX backup_schedules_service_id ON backup_schedules (service_id);
COMMIT;
PRAGMA foreign_keys = ON;

-- +goose Down
PRAGMA foreign_keys = OFF;
BEGIN;
CREATE TABLE backup_schedules_old (
    id           TEXT PRIMARY KEY,
    service_id   TEXT NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    target_id    TEXT NOT NULL REFERENCES backup_targets (id),
    cron         TEXT NOT NULL,
    keep_last    INTEGER NOT NULL DEFAULT 0,
    keep_daily   INTEGER NOT NULL DEFAULT 0,
    keep_weekly  INTEGER NOT NULL DEFAULT 0,
    keep_monthly INTEGER NOT NULL DEFAULT 0,
    enabled      INTEGER NOT NULL DEFAULT 1,
    created_at   TEXT NOT NULL,
    verify       INTEGER NOT NULL DEFAULT 1
) STRICT;
INSERT INTO backup_schedules_old (id, service_id, target_id, cron, keep_last, keep_daily, keep_weekly, keep_monthly, enabled, created_at, verify)
SELECT id, service_id, target_id, cron, keep_last, keep_daily, keep_weekly, keep_monthly, enabled, created_at, verify
FROM backup_schedules WHERE kind = 'postgres';
DROP TABLE backup_schedules;
ALTER TABLE backup_schedules_old RENAME TO backup_schedules;
CREATE INDEX backup_schedules_service_id ON backup_schedules (service_id);
COMMIT;
PRAGMA foreign_keys = ON;
