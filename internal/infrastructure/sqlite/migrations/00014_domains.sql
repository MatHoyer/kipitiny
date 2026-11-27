-- +goose Up
-- Base domains offered when giving a service a public domain.
CREATE TABLE domains (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL
) STRICT;

-- +goose Down
DROP TABLE domains;
