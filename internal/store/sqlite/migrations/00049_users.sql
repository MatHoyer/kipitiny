-- Several users: admins do everything; members only what their grants allow.

-- +goose Up
-- Existing users were all implicit admins. features lists the server-level
-- resources (JSON array) a member may see, read-only.
ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'admin' CHECK (role IN ('admin', 'member'));
ALTER TABLE users ADD COLUMN features TEXT NOT NULL DEFAULT '[]';

-- What a member may do in one project (JSON array of permissions).
CREATE TABLE project_grants (
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    perms      TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL,
    PRIMARY KEY (user_id, project_id)
) STRICT;

CREATE INDEX project_grants_project_id ON project_grants (project_id);

-- A link that creates an account. Only a SHA-256 of its token is stored;
-- grants is a JSON object of project ID to permissions.
CREATE TABLE invites (
    id         TEXT PRIMARY KEY,
    token_hash TEXT NOT NULL UNIQUE,
    role       TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    features   TEXT NOT NULL DEFAULT '[]',
    grants     TEXT NOT NULL DEFAULT '{}',
    created_by TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    used_at    TEXT
) STRICT;

-- Tokens act for their owner, never beyond the owner's permissions.
-- Existing ones go to the oldest user (an admin).
ALTER TABLE api_tokens ADD COLUMN user_id TEXT REFERENCES users (id) ON DELETE CASCADE;
UPDATE api_tokens SET user_id = (SELECT id FROM users ORDER BY created_at, id LIMIT 1);

CREATE INDEX api_tokens_user_id ON api_tokens (user_id);

-- +goose Down
DROP INDEX api_tokens_user_id;
ALTER TABLE api_tokens DROP COLUMN user_id;
DROP TABLE invites;
DROP TABLE project_grants;
ALTER TABLE users DROP COLUMN features;
ALTER TABLE users DROP COLUMN role;
