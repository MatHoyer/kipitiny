-- Git providers give the manager access to private repositories, in place
-- of a token pasted on each project. kind: github (a GitHub App created from
-- a manifest, installed on an account), gitlab or gitea (an OAuth
-- application authorized by a user). Installation tokens are minted in
-- memory; OAuth tokens are kept here and refreshed.
-- +goose Up
CREATE TABLE git_providers (
    id               TEXT PRIMARY KEY,
    kind             TEXT NOT NULL CHECK (kind IN ('github', 'gitlab', 'gitea')),
    name             TEXT NOT NULL UNIQUE,
    base_url         TEXT NOT NULL,
    account          TEXT NOT NULL DEFAULT '',
    app_id           INTEGER NOT NULL DEFAULT 0,
    app_slug         TEXT NOT NULL DEFAULT '',
    private_key      TEXT NOT NULL DEFAULT '',
    installation_id  INTEGER NOT NULL DEFAULT 0,
    webhook_secret   TEXT NOT NULL DEFAULT '',
    client_id        TEXT NOT NULL DEFAULT '',
    client_secret    TEXT NOT NULL DEFAULT '',
    redirect_url     TEXT NOT NULL DEFAULT '',
    access_token     TEXT NOT NULL DEFAULT '',
    refresh_token    TEXT NOT NULL DEFAULT '',
    token_expires_at TEXT,
    created_at       TEXT NOT NULL
) STRICT;

-- A linked project reads its repository through a provider (none: a public
-- repository). Tokens pasted on projects are dropped: such a link fails
-- until a provider is chosen.
ALTER TABLE project_git ADD COLUMN provider_id TEXT REFERENCES git_providers (id);
UPDATE project_git SET last_error = 'Private repositories now need a git provider: add one in Settings › Git providers, then edit the link.'
WHERE token != '';
ALTER TABLE project_git DROP COLUMN token;

-- +goose Down
ALTER TABLE project_git ADD COLUMN token TEXT NOT NULL DEFAULT '';
ALTER TABLE project_git DROP COLUMN provider_id;
DROP TABLE git_providers;
