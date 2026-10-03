-- Passkeys (WebAuthn).

-- +goose Up
-- credential is the WebAuthn credential (public key, sign count) as JSON.
CREATE TABLE passkeys (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    credential_id TEXT NOT NULL UNIQUE,
    credential    TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    last_used_at  TEXT
) STRICT;

CREATE INDEX passkeys_user_id ON passkeys (user_id);

-- +goose Down
DROP TABLE passkeys;
