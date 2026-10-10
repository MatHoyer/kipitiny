-- MySQL, MariaDB and MongoDB services, backed up as a dump made by their own
-- tool (backup kind dump). SQLite can't change a CHECK constraint, so the
-- tables are rebuilt (with foreign keys off: deployments, restores and
-- schedules reference them). Columns keep the live tables' order.

-- +goose NO TRANSACTION
-- +goose Up
PRAGMA foreign_keys = OFF;
BEGIN;
CREATE TABLE services_new (
    id                    TEXT PRIMARY KEY,
    project_id            TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name                  TEXT NOT NULL,
    kind                  TEXT NOT NULL CHECK (kind IN ('app', 'postgres', 'redis', 'mysql', 'mariadb', 'mongodb')),
    image                 TEXT NOT NULL,
    replicas              INTEGER NOT NULL DEFAULT 1,
    created_at            TEXT NOT NULL,
    updated_at            TEXT NOT NULL,
    port                  INTEGER NOT NULL DEFAULT 0,
    domain                TEXT NOT NULL DEFAULT '',
    env                   TEXT NOT NULL DEFAULT '{}',
    memory_mb             INTEGER NOT NULL DEFAULT 0,
    health_path           TEXT NOT NULL DEFAULT '',
    pre_deploy            TEXT NOT NULL DEFAULT '',
    current_deployment_id TEXT NOT NULL DEFAULT '',
    stopped               INTEGER NOT NULL DEFAULT 0,
    server_id             TEXT NOT NULL DEFAULT 'local' REFERENCES servers (id),
    secrets               TEXT NOT NULL DEFAULT '[]',
    cpus                  REAL NOT NULL DEFAULT 0,
    volumes               TEXT NOT NULL DEFAULT '[]',
    pre_backup            TEXT NOT NULL DEFAULT '',
    middlewares           TEXT NOT NULL DEFAULT '{}',
    published_ports       TEXT NOT NULL DEFAULT '[]',
    stop_grace_seconds    INTEGER NOT NULL DEFAULT 0,
    icon                  TEXT NOT NULL DEFAULT '',
    git_spec_hash         TEXT NOT NULL DEFAULT '',
    orphaned              INTEGER NOT NULL DEFAULT 0,
    host_network          INTEGER NOT NULL DEFAULT 0,
    docker_socket         TEXT NOT NULL DEFAULT '' CHECK (docker_socket IN ('', 'ro', 'rw')),
    healthcheck           TEXT NOT NULL DEFAULT '{}',
    networks              TEXT NOT NULL DEFAULT '[]',
    UNIQUE (project_id, name)
) STRICT;
INSERT INTO services_new SELECT * FROM services;
DROP TABLE services;
ALTER TABLE services_new RENAME TO services;
CREATE INDEX services_project_id ON services (project_id);
CREATE UNIQUE INDEX services_domain ON services (domain) WHERE domain <> '';

CREATE TABLE backups_new (
    id             TEXT PRIMARY KEY,
    kind           TEXT NOT NULL DEFAULT 'postgres' CHECK (kind IN ('postgres', 'manager', 'volume', 'dump')),
    service_id     TEXT NOT NULL,
    project_id     TEXT NOT NULL,
    service_name   TEXT NOT NULL,
    project_name   TEXT NOT NULL,
    target_id      TEXT NOT NULL REFERENCES backup_targets (id),
    schedule_id    TEXT NOT NULL DEFAULT '',
    object_key     TEXT NOT NULL,
    status         TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
    size_bytes     INTEGER NOT NULL DEFAULT 0,
    sha256         TEXT NOT NULL DEFAULT '',
    encrypted      INTEGER NOT NULL DEFAULT 0,
    pg_version     TEXT NOT NULL DEFAULT '',
    volumes        TEXT NOT NULL DEFAULT '[]',
    duration_ms    INTEGER NOT NULL DEFAULT 0,
    error          TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    finished_at    TEXT,
    verify_status  TEXT NOT NULL DEFAULT '' CHECK (verify_status IN ('', 'running', 'succeeded', 'failed')),
    verify_error   TEXT NOT NULL DEFAULT '',
    verify_details TEXT NOT NULL DEFAULT '{}',
    verified_at    TEXT,
    service_kind   TEXT NOT NULL DEFAULT '',
    service_icon   TEXT NOT NULL DEFAULT '',
    db_name        TEXT NOT NULL DEFAULT ''
) STRICT;
INSERT INTO backups_new SELECT * FROM backups;
DROP TABLE backups;
ALTER TABLE backups_new RENAME TO backups;
CREATE INDEX backups_service_id ON backups (service_id, created_at);

CREATE TABLE backup_schedules_new (
    id           TEXT PRIMARY KEY,
    kind         TEXT NOT NULL DEFAULT 'postgres' CHECK (kind IN ('postgres', 'manager', 'volume', 'dump')),
    service_id   TEXT REFERENCES services (id) ON DELETE CASCADE,
    target_id    TEXT NOT NULL REFERENCES backup_targets (id),
    cron         TEXT NOT NULL,
    keep_last    INTEGER NOT NULL DEFAULT 0,
    keep_daily   INTEGER NOT NULL DEFAULT 0,
    keep_weekly  INTEGER NOT NULL DEFAULT 0,
    keep_monthly INTEGER NOT NULL DEFAULT 0,
    enabled      INTEGER NOT NULL DEFAULT 1,
    created_at   TEXT NOT NULL,
    verify       INTEGER NOT NULL DEFAULT 1,
    db_name      TEXT NOT NULL DEFAULT '',
    CHECK ((kind = 'manager') = (service_id IS NULL))
) STRICT;
INSERT INTO backup_schedules_new SELECT * FROM backup_schedules;
DROP TABLE backup_schedules;
ALTER TABLE backup_schedules_new RENAME TO backup_schedules;
CREATE INDEX backup_schedules_service_id ON backup_schedules (service_id);
COMMIT;
PRAGMA foreign_keys = ON;

-- +goose Down
-- Foreign keys still on: their deployments and schedules go with them.
DELETE FROM services WHERE kind IN ('mysql', 'mariadb', 'mongodb');
DELETE FROM backups WHERE kind = 'dump';
PRAGMA foreign_keys = OFF;
BEGIN;
CREATE TABLE services_old (
    id                    TEXT PRIMARY KEY,
    project_id            TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name                  TEXT NOT NULL,
    kind                  TEXT NOT NULL CHECK (kind IN ('app', 'postgres', 'redis')),
    image                 TEXT NOT NULL,
    replicas              INTEGER NOT NULL DEFAULT 1,
    created_at            TEXT NOT NULL,
    updated_at            TEXT NOT NULL,
    port                  INTEGER NOT NULL DEFAULT 0,
    domain                TEXT NOT NULL DEFAULT '',
    env                   TEXT NOT NULL DEFAULT '{}',
    memory_mb             INTEGER NOT NULL DEFAULT 0,
    health_path           TEXT NOT NULL DEFAULT '',
    pre_deploy            TEXT NOT NULL DEFAULT '',
    current_deployment_id TEXT NOT NULL DEFAULT '',
    stopped               INTEGER NOT NULL DEFAULT 0,
    server_id             TEXT NOT NULL DEFAULT 'local' REFERENCES servers (id),
    secrets               TEXT NOT NULL DEFAULT '[]',
    cpus                  REAL NOT NULL DEFAULT 0,
    volumes               TEXT NOT NULL DEFAULT '[]',
    pre_backup            TEXT NOT NULL DEFAULT '',
    middlewares           TEXT NOT NULL DEFAULT '{}',
    published_ports       TEXT NOT NULL DEFAULT '[]',
    stop_grace_seconds    INTEGER NOT NULL DEFAULT 0,
    icon                  TEXT NOT NULL DEFAULT '',
    git_spec_hash         TEXT NOT NULL DEFAULT '',
    orphaned              INTEGER NOT NULL DEFAULT 0,
    host_network          INTEGER NOT NULL DEFAULT 0,
    docker_socket         TEXT NOT NULL DEFAULT '' CHECK (docker_socket IN ('', 'ro', 'rw')),
    healthcheck           TEXT NOT NULL DEFAULT '{}',
    networks              TEXT NOT NULL DEFAULT '[]',
    UNIQUE (project_id, name)
) STRICT;
INSERT INTO services_old SELECT * FROM services;
DROP TABLE services;
ALTER TABLE services_old RENAME TO services;
CREATE INDEX services_project_id ON services (project_id);
CREATE UNIQUE INDEX services_domain ON services (domain) WHERE domain <> '';

CREATE TABLE backups_old (
    id             TEXT PRIMARY KEY,
    kind           TEXT NOT NULL DEFAULT 'postgres' CHECK (kind IN ('postgres', 'manager', 'volume')),
    service_id     TEXT NOT NULL,
    project_id     TEXT NOT NULL,
    service_name   TEXT NOT NULL,
    project_name   TEXT NOT NULL,
    target_id      TEXT NOT NULL REFERENCES backup_targets (id),
    schedule_id    TEXT NOT NULL DEFAULT '',
    object_key     TEXT NOT NULL,
    status         TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
    size_bytes     INTEGER NOT NULL DEFAULT 0,
    sha256         TEXT NOT NULL DEFAULT '',
    encrypted      INTEGER NOT NULL DEFAULT 0,
    pg_version     TEXT NOT NULL DEFAULT '',
    volumes        TEXT NOT NULL DEFAULT '[]',
    duration_ms    INTEGER NOT NULL DEFAULT 0,
    error          TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    finished_at    TEXT,
    verify_status  TEXT NOT NULL DEFAULT '' CHECK (verify_status IN ('', 'running', 'succeeded', 'failed')),
    verify_error   TEXT NOT NULL DEFAULT '',
    verify_details TEXT NOT NULL DEFAULT '{}',
    verified_at    TEXT,
    service_kind   TEXT NOT NULL DEFAULT '',
    service_icon   TEXT NOT NULL DEFAULT '',
    db_name        TEXT NOT NULL DEFAULT ''
) STRICT;
INSERT INTO backups_old SELECT * FROM backups;
DROP TABLE backups;
ALTER TABLE backups_old RENAME TO backups;
CREATE INDEX backups_service_id ON backups (service_id, created_at);

CREATE TABLE backup_schedules_old (
    id           TEXT PRIMARY KEY,
    kind         TEXT NOT NULL DEFAULT 'postgres' CHECK (kind IN ('postgres', 'manager', 'volume')),
    service_id   TEXT REFERENCES services (id) ON DELETE CASCADE,
    target_id    TEXT NOT NULL REFERENCES backup_targets (id),
    cron         TEXT NOT NULL,
    keep_last    INTEGER NOT NULL DEFAULT 0,
    keep_daily   INTEGER NOT NULL DEFAULT 0,
    keep_weekly  INTEGER NOT NULL DEFAULT 0,
    keep_monthly INTEGER NOT NULL DEFAULT 0,
    enabled      INTEGER NOT NULL DEFAULT 1,
    created_at   TEXT NOT NULL,
    verify       INTEGER NOT NULL DEFAULT 1,
    db_name      TEXT NOT NULL DEFAULT '',
    CHECK ((kind = 'manager') = (service_id IS NULL))
) STRICT;
INSERT INTO backup_schedules_old SELECT * FROM backup_schedules;
DROP TABLE backup_schedules;
ALTER TABLE backup_schedules_old RENAME TO backup_schedules;
CREATE INDEX backup_schedules_service_id ON backup_schedules (service_id);
COMMIT;
PRAGMA foreign_keys = ON;
