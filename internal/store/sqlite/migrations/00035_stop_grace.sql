-- +goose Up
-- Seconds an app gets to exit on stop before it is killed; 0 is the default.
ALTER TABLE services ADD COLUMN stop_grace_seconds INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE services DROP COLUMN stop_grace_seconds;
