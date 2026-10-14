-- Email providers: SMTP servers email goes out through. Invites use the one
-- the email_invites setting names; each email channel names its own. The
-- single smtp setting before them becomes a provider at startup.
-- +goose Up
CREATE TABLE email_providers (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    host       TEXT NOT NULL,
    port       INTEGER NOT NULL,
    security   TEXT NOT NULL,
    username   TEXT NOT NULL DEFAULT '',
    password   TEXT NOT NULL DEFAULT '',
    sender     TEXT NOT NULL,
    created_at TEXT NOT NULL
) STRICT;

-- +goose Down
DROP TABLE email_providers;
