-- The last results of each uptime check (a JSON array), so they survive
-- a manager restart.
-- +goose Up
ALTER TABLE uptime_checks ADD COLUMN recent TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE uptime_checks DROP COLUMN recent;
