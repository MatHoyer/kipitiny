-- +goose Up
CREATE TABLE api_tokens (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    scope        TEXT NOT NULL CHECK (scope IN ('read', 'deploy', 'admin')),
    created_at   TEXT NOT NULL,
    last_used_at TEXT
) STRICT;

CREATE TABLE audit_log (
    id         TEXT PRIMARY KEY,
    actor      TEXT NOT NULL,
    action     TEXT NOT NULL,
    target     TEXT NOT NULL DEFAULT '',
    status     INTEGER NOT NULL,
    error      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
) STRICT;

CREATE INDEX audit_log_created_at ON audit_log (created_at);

-- +goose Down
DROP TABLE audit_log;
DROP TABLE api_tokens;
