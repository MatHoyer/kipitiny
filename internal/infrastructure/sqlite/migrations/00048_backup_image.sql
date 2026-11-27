-- +goose Up
-- The backed-up service's image, and the PostgreSQL extensions the dump uses
-- (JSON array): restore tests and restores need a server that has them.
ALTER TABLE backups ADD COLUMN image TEXT NOT NULL DEFAULT '';
ALTER TABLE backups ADD COLUMN extensions TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE backups DROP COLUMN extensions;
ALTER TABLE backups DROP COLUMN image;
