-- +goose Up
-- A user's own UI choices (JSON store.Preferences), like the map's view.
ALTER TABLE users ADD COLUMN preferences TEXT NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE users DROP COLUMN preferences;
