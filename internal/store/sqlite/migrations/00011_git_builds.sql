-- +goose Up
ALTER TABLE services ADD COLUMN source TEXT NOT NULL DEFAULT 'image' CHECK (source IN ('image', 'git'));
ALTER TABLE services ADD COLUMN git_url TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN git_branch TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN git_token TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN dockerfile TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN build_context TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN webhook_secret TEXT NOT NULL DEFAULT '';

ALTER TABLE deployments ADD COLUMN git_commit TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE deployments DROP COLUMN git_commit;
ALTER TABLE services DROP COLUMN webhook_secret;
ALTER TABLE services DROP COLUMN build_context;
ALTER TABLE services DROP COLUMN dockerfile;
ALTER TABLE services DROP COLUMN git_token;
ALTER TABLE services DROP COLUMN git_branch;
ALTER TABLE services DROP COLUMN git_url;
ALTER TABLE services DROP COLUMN source;
