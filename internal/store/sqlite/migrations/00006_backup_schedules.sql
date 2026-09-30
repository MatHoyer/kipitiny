-- +goose Up
CREATE TABLE backup_schedules (
    id           TEXT PRIMARY KEY,
    service_id   TEXT NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    target_id    TEXT NOT NULL REFERENCES backup_targets (id),
    cron         TEXT NOT NULL,
    keep_last    INTEGER NOT NULL DEFAULT 0,
    keep_daily   INTEGER NOT NULL DEFAULT 0,
    keep_weekly  INTEGER NOT NULL DEFAULT 0,
    keep_monthly INTEGER NOT NULL DEFAULT 0,
    enabled      INTEGER NOT NULL DEFAULT 1,
    created_at   TEXT NOT NULL
) STRICT;

CREATE INDEX backup_schedules_service_id ON backup_schedules (service_id);

-- Retention only ever prunes backups made by the same schedule.
ALTER TABLE backups ADD COLUMN schedule_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE backups DROP COLUMN schedule_id;
DROP TABLE backup_schedules;
