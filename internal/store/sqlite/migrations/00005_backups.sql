-- +goose Up
CREATE TABLE backup_targets (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    kind       TEXT NOT NULL CHECK (kind IN ('local', 's3')),
    endpoint   TEXT NOT NULL DEFAULT '',
    region     TEXT NOT NULL DEFAULT '',
    bucket     TEXT NOT NULL DEFAULT '',
    prefix     TEXT NOT NULL DEFAULT '',
    access_key TEXT NOT NULL DEFAULT '',
    secret_key TEXT NOT NULL DEFAULT '',
    use_ssl    INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL
) STRICT;

-- The data volume always exists, so local disk is always a target.
INSERT INTO backup_targets (id, name, kind, created_at)
VALUES ('local', 'Local disk', 'local', strftime('%Y-%m-%d %H:%M:%f+00:00', 'now'));

-- Backups outlive their service: no foreign key to services, and the names
-- are copied so the list stays readable after a database is deleted.
CREATE TABLE backups (
    id           TEXT PRIMARY KEY,
    service_id   TEXT NOT NULL,
    project_id   TEXT NOT NULL,
    service_name TEXT NOT NULL,
    project_name TEXT NOT NULL,
    target_id    TEXT NOT NULL REFERENCES backup_targets (id),
    object_key   TEXT NOT NULL,
    status       TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
    size_bytes   INTEGER NOT NULL DEFAULT 0,
    sha256       TEXT NOT NULL DEFAULT '',
    pg_version   TEXT NOT NULL DEFAULT '',
    duration_ms  INTEGER NOT NULL DEFAULT 0,
    error        TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    finished_at  TEXT
) STRICT;

CREATE INDEX backups_service_id ON backups (service_id, created_at);

CREATE TABLE restores (
    id          TEXT PRIMARY KEY,
    backup_id   TEXT NOT NULL REFERENCES backups (id) ON DELETE CASCADE,
    service_id  TEXT NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    status      TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
    error       TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    finished_at TEXT
) STRICT;

CREATE INDEX restores_service_id ON restores (service_id, created_at);

-- +goose Down
DROP TABLE restores;
DROP TABLE backups;
DROP TABLE backup_targets;
