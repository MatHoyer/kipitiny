-- +goose Up
ALTER TABLE services ADD COLUMN health_path TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN pre_deploy TEXT NOT NULL DEFAULT '';
-- The deployment whose containers serve traffic; others are retired.
ALTER TABLE services ADD COLUMN current_deployment_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE services DROP COLUMN current_deployment_id;
ALTER TABLE services DROP COLUMN pre_deploy;
ALTER TABLE services DROP COLUMN health_path;
