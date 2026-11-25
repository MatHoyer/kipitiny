-- +goose Up
-- The app's own tracker script and hit paths on its domain, so ad blockers
-- don't recognize them; empty for /a.js and /e of the analytics domain.
ALTER TABLE services ADD COLUMN analytics_script TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN analytics_hit TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE services DROP COLUMN analytics_hit;
ALTER TABLE services DROP COLUMN analytics_script;
