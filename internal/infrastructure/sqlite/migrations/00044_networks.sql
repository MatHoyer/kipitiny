-- +goose Up
-- Networks created by hand on a server, next to the automatic proxy and
-- project networks; services of any project on that server can join them.
CREATE TABLE networks (
    id         TEXT PRIMARY KEY,
    server_id  TEXT NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (server_id, name)
) STRICT;

-- IDs of the networks a service joins, as JSON.
ALTER TABLE services ADD COLUMN networks TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE services DROP COLUMN networks;
DROP TABLE networks;
