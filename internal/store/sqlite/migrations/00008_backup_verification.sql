-- +goose Up
-- Result of the latest restore test of each backup.
ALTER TABLE backups ADD COLUMN verify_status TEXT NOT NULL DEFAULT '' CHECK (verify_status IN ('', 'running', 'succeeded', 'failed'));
ALTER TABLE backups ADD COLUMN verify_error TEXT NOT NULL DEFAULT '';
ALTER TABLE backups ADD COLUMN verify_details TEXT NOT NULL DEFAULT '{}';
ALTER TABLE backups ADD COLUMN verified_at TEXT;

ALTER TABLE backup_schedules ADD COLUMN verify INTEGER NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE backup_schedules DROP COLUMN verify;
ALTER TABLE backups DROP COLUMN verified_at;
ALTER TABLE backups DROP COLUMN verify_details;
ALTER TABLE backups DROP COLUMN verify_error;
ALTER TABLE backups DROP COLUMN verify_status;
