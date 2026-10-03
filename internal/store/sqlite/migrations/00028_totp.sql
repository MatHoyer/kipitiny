-- Two-factor authentication (TOTP).

-- +goose Up
-- totp_secret is empty while TOTP is off. totp_last_step is the last
-- accepted time step, so a code can't be replayed.
ALTER TABLE users ADD COLUMN totp_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN totp_last_step INTEGER NOT NULL DEFAULT 0;

-- One-time codes for when the authenticator is lost; only a SHA-256 of each
-- is stored, and a used code is deleted.
CREATE TABLE recovery_codes (
    code_hash  TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TEXT NOT NULL
) STRICT;

CREATE INDEX recovery_codes_user_id ON recovery_codes (user_id);

-- +goose Down
DROP TABLE recovery_codes;
ALTER TABLE users DROP COLUMN totp_last_step;
ALTER TABLE users DROP COLUMN totp_secret;
