-- +goose Up
-- The app's domain is tracked by the analytics server (when analytics are on).
ALTER TABLE services ADD COLUMN analytics INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE services DROP COLUMN analytics;
