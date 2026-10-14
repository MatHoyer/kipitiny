-- Single sign-on: OpenID Connect providers users sign in with. name labels
-- the sign-in button; the issuer's discovery document gives the endpoints.
-- +goose Up
CREATE TABLE sso_providers (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    issuer        TEXT NOT NULL,
    client_id     TEXT NOT NULL,
    client_secret TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL
) STRICT;

-- +goose Down
DROP TABLE sso_providers;
