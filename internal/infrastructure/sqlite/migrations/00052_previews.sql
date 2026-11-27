-- +goose Up
-- Preview environments: a project copied from a parent project (parent_id)
-- for a branch or a stack of pull requests. preview is JSON (see
-- store.Preview); both are empty for ordinary projects.
ALTER TABLE projects ADD COLUMN parent_id TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN preview TEXT NOT NULL DEFAULT '{}';

CREATE INDEX projects_parent_id ON projects (parent_id);

-- How a project's previews differ from it: their base domain, the values
-- they get instead of the project's secrets (env, secrets: JSON), the
-- project secrets they may still use (inherit: JSON array), how long an
-- unused one lives, and the idle time before its apps sleep.
CREATE TABLE preview_settings (
    project_id          TEXT PRIMARY KEY REFERENCES projects (id) ON DELETE CASCADE,
    base_domain         TEXT NOT NULL DEFAULT '',
    env                 TEXT NOT NULL DEFAULT '{}',
    secrets             TEXT NOT NULL DEFAULT '[]',
    inherit             TEXT NOT NULL DEFAULT '[]',
    ttl_seconds         INTEGER NOT NULL DEFAULT 0,
    sleep_after_seconds INTEGER NOT NULL DEFAULT 0,
    allow_seed          INTEGER NOT NULL DEFAULT 0,
    updated_at          TEXT NOT NULL
) STRICT;

-- +goose Down
DROP TABLE preview_settings;
DROP INDEX projects_parent_id;
ALTER TABLE projects DROP COLUMN preview;
ALTER TABLE projects DROP COLUMN parent_id;
