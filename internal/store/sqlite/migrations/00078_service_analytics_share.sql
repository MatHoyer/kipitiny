-- +goose Up
-- An app's public, read-only analytics link: the SHA-256 of its token (the
-- token itself is only shown when created), empty when not shared.
ALTER TABLE services ADD COLUMN analytics_share TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN analytics_shared_at TEXT;
CREATE UNIQUE INDEX services_analytics_share ON services (analytics_share) WHERE analytics_share <> '';

-- +goose Down
DROP INDEX services_analytics_share;
ALTER TABLE services DROP COLUMN analytics_shared_at;
ALTER TABLE services DROP COLUMN analytics_share;
