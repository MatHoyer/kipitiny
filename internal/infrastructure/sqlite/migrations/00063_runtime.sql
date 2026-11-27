-- +goose Up
-- An app's command, entrypoint, user, tmpfs… from compose, as JSON (see
-- store.Runtime); '{}' runs the image as built.
ALTER TABLE services ADD COLUMN runtime TEXT NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE services DROP COLUMN runtime;
