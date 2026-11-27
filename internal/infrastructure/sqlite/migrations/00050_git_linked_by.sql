-- +goose Up
-- A sync acts as the user who linked the repository, so a member can't
-- push changes they couldn't make themselves. Empty for links made while
-- only admins existed.
ALTER TABLE project_git ADD COLUMN linked_by TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE project_git DROP COLUMN linked_by;
