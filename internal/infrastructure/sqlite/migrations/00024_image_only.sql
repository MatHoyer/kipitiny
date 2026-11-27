-- +goose Up
-- Services run Docker images only; builds from Git are gone. A former git
-- service keeps the image it last deployed, if any.
UPDATE services
SET image = COALESCE(NULLIF((SELECT d.image FROM deployments d WHERE d.id = services.current_deployment_id), ''), image)
WHERE source = 'git';

ALTER TABLE services DROP COLUMN webhook_secret;
ALTER TABLE services DROP COLUMN build_context;
ALTER TABLE services DROP COLUMN dockerfile;
ALTER TABLE services DROP COLUMN git_token;
ALTER TABLE services DROP COLUMN git_branch;
ALTER TABLE services DROP COLUMN git_url;
ALTER TABLE services DROP COLUMN source;

-- +goose Down
ALTER TABLE services ADD COLUMN source TEXT NOT NULL DEFAULT 'image' CHECK (source IN ('image', 'git'));
ALTER TABLE services ADD COLUMN git_url TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN git_branch TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN git_token TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN dockerfile TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN build_context TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN webhook_secret TEXT NOT NULL DEFAULT '';
