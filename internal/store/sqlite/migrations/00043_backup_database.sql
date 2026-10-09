-- +goose Up
-- The PostgreSQL database a backup holds, or a schedule backs up: one of the
-- instance's. Empty is the service's own (POSTGRES_DB), as before.
ALTER TABLE backups ADD COLUMN db_name TEXT NOT NULL DEFAULT '';
ALTER TABLE backup_schedules ADD COLUMN db_name TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE backup_schedules DROP COLUMN db_name;
ALTER TABLE backups DROP COLUMN db_name;
