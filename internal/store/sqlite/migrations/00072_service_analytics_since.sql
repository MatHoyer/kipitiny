-- +goose Up
-- When the service's current domain started being counted: analytics of the
-- hostname from before belong to whoever held it then. Empty for services
-- that had their domain before this column.
ALTER TABLE services ADD COLUMN analytics_since TEXT;

-- +goose Down
ALTER TABLE services DROP COLUMN analytics_since;
