-- +goose Up
-- The services of the project a service starts after, as JSON (see
-- store.Dependency).
ALTER TABLE services ADD COLUMN depends_on TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE services DROP COLUMN depends_on;
