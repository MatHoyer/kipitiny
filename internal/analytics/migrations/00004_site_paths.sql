-- +goose Up
-- A site's own tracker script and hit paths on its domain; empty for
-- /a.js and /e of the analytics domain only.
ALTER TABLE sites ADD COLUMN script TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN hit TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE sites DROP COLUMN hit;
ALTER TABLE sites DROP COLUMN script;
