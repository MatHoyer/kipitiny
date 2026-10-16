-- +goose Up
-- The project's own networks a service is on, by compose name, as JSON;
-- empty is the default (project) network only.
ALTER TABLE services ADD COLUMN project_networks TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE services DROP COLUMN project_networks;
