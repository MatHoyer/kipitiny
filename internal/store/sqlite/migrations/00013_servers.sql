-- +goose Up
CREATE TABLE servers (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    kind        TEXT NOT NULL CHECK (kind IN ('local', 'ssh')),
    host        TEXT NOT NULL DEFAULT '',
    port        INTEGER NOT NULL DEFAULT 22,
    ssh_user    TEXT NOT NULL DEFAULT '',
    -- Docker socket path on the server.
    socket      TEXT NOT NULL DEFAULT '/var/run/docker.sock',
    -- Pinned SSH host key (authorized_keys format), trusted on first use.
    host_key    TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL
) STRICT;

INSERT INTO servers (id, name, kind, created_at)
VALUES ('local', 'This server', 'local', strftime('%Y-%m-%d %H:%M:%f+00:00', 'now'));

-- A project's services share a private network, so they live on one server.
ALTER TABLE projects ADD COLUMN server_id TEXT NOT NULL DEFAULT 'local' REFERENCES servers (id);
ALTER TABLE services ADD COLUMN server_id TEXT NOT NULL DEFAULT 'local' REFERENCES servers (id);

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;

-- +goose Down
DROP TABLE settings;
ALTER TABLE services DROP COLUMN server_id;
ALTER TABLE projects DROP COLUMN server_id;
DROP TABLE servers;
