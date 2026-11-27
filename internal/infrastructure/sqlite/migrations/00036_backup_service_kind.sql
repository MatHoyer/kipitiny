-- +goose Up
-- The backed-up service's kind (app, postgres, redis), kept like its name so
-- backups of deleted services still show what they hold. Empty for the
-- manager's own backups.
ALTER TABLE backups ADD COLUMN service_kind TEXT NOT NULL DEFAULT '';
UPDATE backups SET service_kind = COALESCE((SELECT kind FROM services WHERE services.id = backups.service_id), '');
UPDATE backups SET service_kind = 'postgres' WHERE service_kind = '' AND kind = 'postgres';

-- +goose Down
ALTER TABLE backups DROP COLUMN service_kind;
