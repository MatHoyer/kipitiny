-- +goose Up
-- Who started a deployment: user:<name>, token:<name> or webhook:<source>.
ALTER TABLE deployments ADD COLUMN triggered_by TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE deployments DROP COLUMN triggered_by;
