-- +goose Up
-- Encryption is chosen when a target is created and never changes, so every
-- backup on a target can be decrypted with the target's identity.
ALTER TABLE backup_targets ADD COLUMN age_recipient TEXT NOT NULL DEFAULT '';
ALTER TABLE backup_targets ADD COLUMN age_identity TEXT NOT NULL DEFAULT '';

ALTER TABLE backups ADD COLUMN kind TEXT NOT NULL DEFAULT 'postgres' CHECK (kind IN ('postgres', 'manager'));
ALTER TABLE backups ADD COLUMN encrypted INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE backups DROP COLUMN encrypted;
ALTER TABLE backups DROP COLUMN kind;
ALTER TABLE backup_targets DROP COLUMN age_identity;
ALTER TABLE backup_targets DROP COLUMN age_recipient;
