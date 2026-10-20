-- +goose Up
-- A repository linked with a restricted token syncs within that token's
-- restriction, not with all of its owner's permissions.
ALTER TABLE project_git ADD COLUMN linked_token TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE project_git DROP COLUMN linked_token;
