-- +goose Up
-- Files written into an app's containers before they start (compose
-- configs, secrets, files bind-mounted from git), as JSON (see
-- store.ServiceFile).
ALTER TABLE services ADD COLUMN files TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE services DROP COLUMN files;
