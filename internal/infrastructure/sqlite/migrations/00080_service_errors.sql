-- +goose Up
-- Error tracking: the app's Sentry SDK reports to the analytics server.
-- errors_project and errors_key make its DSN; kept while off.
ALTER TABLE services ADD COLUMN errors INTEGER NOT NULL DEFAULT 0;
ALTER TABLE services ADD COLUMN errors_project INTEGER NOT NULL DEFAULT 0;
ALTER TABLE services ADD COLUMN errors_key TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE services DROP COLUMN errors_key;
ALTER TABLE services DROP COLUMN errors_project;
ALTER TABLE services DROP COLUMN errors;
