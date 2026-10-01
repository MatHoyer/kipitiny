-- +goose Up
-- Credentials for private image registries, sent with every pull from that
-- host (docker.io for Docker Hub).
CREATE TABLE registries (
    id         TEXT PRIMARY KEY,
    host       TEXT NOT NULL UNIQUE,
    username   TEXT NOT NULL,
    password   TEXT NOT NULL,
    created_at TEXT NOT NULL
) STRICT;

-- +goose Down
DROP TABLE registries;
