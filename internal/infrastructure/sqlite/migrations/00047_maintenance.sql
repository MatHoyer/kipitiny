-- +goose Up
-- An app's maintenance page and mode, as JSON (see store.Maintenance).
ALTER TABLE services ADD COLUMN maintenance TEXT NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE services DROP COLUMN maintenance;
