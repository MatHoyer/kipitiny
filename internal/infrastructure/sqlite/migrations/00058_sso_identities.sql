-- An identity at an SSO provider (its subject) linked to a user, who signs
-- in with it. One per provider and user.
-- +goose Up
CREATE TABLE sso_identities (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    provider_id  TEXT NOT NULL REFERENCES sso_providers (id) ON DELETE CASCADE,
    subject      TEXT NOT NULL,
    email        TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    last_used_at TEXT,
    UNIQUE (provider_id, subject),
    UNIQUE (user_id, provider_id)
) STRICT;

-- +goose Down
DROP TABLE sso_identities;
