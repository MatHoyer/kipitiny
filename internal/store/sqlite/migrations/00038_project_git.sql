-- +goose Up
-- A project following a compose file in a git repository. token is an
-- HTTPS access token (empty for public repositories). applied maps each
-- service to the image the last sync gave it (JSON), to show tag
-- overrides; warnings are the last sync's (JSON list).
CREATE TABLE project_git (
    project_id     TEXT PRIMARY KEY REFERENCES projects (id) ON DELETE CASCADE,
    repo_url       TEXT NOT NULL,
    branch         TEXT NOT NULL,
    path           TEXT NOT NULL,
    token          TEXT NOT NULL DEFAULT '',
    auto_sync      INTEGER NOT NULL DEFAULT 1,
    poll_seconds   INTEGER NOT NULL DEFAULT 300,
    webhook_secret TEXT NOT NULL,
    last_commit    TEXT NOT NULL DEFAULT '',
    last_synced_at TEXT,
    last_error     TEXT NOT NULL DEFAULT '',
    warnings       TEXT NOT NULL DEFAULT '[]',
    applied        TEXT NOT NULL DEFAULT '{}',
    created_at     TEXT NOT NULL
) STRICT;

-- git_spec_hash is the hash of the compose block a git sync last applied:
-- a sync leaves a service alone while it is unchanged (keeping a tag
-- deployed since). orphaned: a database the compose file no longer lists.
ALTER TABLE services ADD COLUMN git_spec_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN orphaned INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE services DROP COLUMN orphaned;
ALTER TABLE services DROP COLUMN git_spec_hash;
DROP TABLE project_git;
