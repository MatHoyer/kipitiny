-- +goose Up
-- The logo the UI shows for a service (e.g. ghost), set by templates or the
-- user; empty picks one from the kind or image. Backups keep the service's
-- icon hint like its name.
ALTER TABLE services ADD COLUMN icon TEXT NOT NULL DEFAULT '';
ALTER TABLE backups ADD COLUMN service_icon TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE backups DROP COLUMN service_icon;
ALTER TABLE services DROP COLUMN icon;
