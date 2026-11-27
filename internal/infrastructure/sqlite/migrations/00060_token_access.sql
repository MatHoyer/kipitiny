-- +goose Up
-- A restricted token only gets the features and project permissions picked
-- at creation (JSON, like a member's), and never more than its owner holds.
ALTER TABLE api_tokens ADD COLUMN restricted INTEGER NOT NULL DEFAULT 0;
ALTER TABLE api_tokens ADD COLUMN features TEXT NOT NULL DEFAULT '[]';
ALTER TABLE api_tokens ADD COLUMN grants TEXT NOT NULL DEFAULT '{}';

-- This replaces scopes: read and deploy tokens become restricted tokens with
-- what their scope allowed, on the projects their owner could reach then.
UPDATE api_tokens SET
    restricted = 1,
    features = '["storage","registries","git-providers","secret-providers","servers","networks","notifications","domains"]',
    grants = (
        SELECT json_group_object(p.id, json(CASE api_tokens.scope
            WHEN 'read' THEN '["view","data"]'
            ELSE '["view","deploy","backups","data"]' END))
        FROM projects p
        WHERE p.parent_id = '' AND (
            api_tokens.user_id IS NULL
            OR EXISTS (SELECT 1 FROM users u WHERE u.id = api_tokens.user_id AND u.role = 'admin')
            OR EXISTS (SELECT 1 FROM project_grants g WHERE g.user_id = api_tokens.user_id AND g.project_id = p.id))
    )
WHERE scope IN ('read', 'deploy');
ALTER TABLE api_tokens DROP COLUMN scope;

-- +goose Down
-- Restricted tokens can't be told apart from read-only ones any more.
ALTER TABLE api_tokens ADD COLUMN scope TEXT NOT NULL DEFAULT 'admin' CHECK (scope IN ('read', 'deploy', 'admin'));
UPDATE api_tokens SET scope = 'read' WHERE restricted = 1;
ALTER TABLE api_tokens DROP COLUMN grants;
ALTER TABLE api_tokens DROP COLUMN features;
ALTER TABLE api_tokens DROP COLUMN restricted;
