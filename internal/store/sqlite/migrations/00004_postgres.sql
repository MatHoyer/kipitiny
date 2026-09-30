-- +goose Up
ALTER TABLE services ADD COLUMN memory_mb INTEGER NOT NULL DEFAULT 0;
-- For apps: the postgres service whose DATABASE_URL is injected.
ALTER TABLE services ADD COLUMN database_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE services DROP COLUMN database_id;
ALTER TABLE services DROP COLUMN memory_mb;
