-- +goose Up
-- Variables shared by the project's services, referenced from a service env
-- as {{ project.NAME }}.
ALTER TABLE projects ADD COLUMN env TEXT NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE projects DROP COLUMN env;
