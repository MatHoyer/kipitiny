-- +goose Up
-- Password manager connections: several per kind (two Proton Pass accounts).
-- The one with an empty name is the default, referenced by the plain scheme
-- (pass://); the others add their name to it (pass+work://). The token of
-- the connection, kept in a setting until now, becomes the default one.
CREATE TABLE secret_connections (
    id         TEXT PRIMARY KEY,
    kind       TEXT NOT NULL,
    name       TEXT NOT NULL,
    token      TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (kind, name)
) STRICT;

-- A ULID: 26 characters of hex are valid Crockford base32.
INSERT INTO secret_connections (id, kind, name, token, created_at)
SELECT '01' || upper(hex(randomblob(12))), 'protonpass', '', value, strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')
FROM settings WHERE key = 'secrets_protonpass_token' AND value != '';
DELETE FROM settings WHERE key = 'secrets_protonpass_token';

-- +goose Down
INSERT INTO settings (key, value)
SELECT 'secrets_protonpass_token', token FROM secret_connections WHERE kind = 'protonpass' AND name = '';
DROP TABLE secret_connections;
