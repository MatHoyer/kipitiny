-- +goose Up
-- Named volumes an app mounts, as JSON [{"name": ..., "path": ...}].
ALTER TABLE services ADD COLUMN volumes TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE services DROP COLUMN volumes;
