-- +goose Up
-- Names of the env entries that are secrets (write-only); the others are
-- plain variables, readable through the API. Every value was masked until
-- now, so existing entries start as secrets.
ALTER TABLE projects ADD COLUMN secrets TEXT NOT NULL DEFAULT '[]';
ALTER TABLE services ADD COLUMN secrets TEXT NOT NULL DEFAULT '[]';
UPDATE projects SET secrets = (SELECT json_group_array(key) FROM json_each(projects.env));
UPDATE services SET secrets = (SELECT json_group_array(key) FROM json_each(services.env));

-- +goose Down
ALTER TABLE services DROP COLUMN secrets;
ALTER TABLE projects DROP COLUMN secrets;
