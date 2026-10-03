-- Redis services. SQLite can't change a CHECK constraint, so the table is
-- rebuilt (with foreign keys off: deployments, backups and schedules
-- reference it).

-- +goose NO TRANSACTION
-- +goose Up
PRAGMA foreign_keys = OFF;
BEGIN;
CREATE TABLE services_new (
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
    UNIQUE (project_id, name)
) STRICT;
INSERT INTO services_new (id, project_id, name, kind, image, replicas, created_at, updated_at, port, domain, env, memory_mb, health_path, pre_deploy, current_deployment_id, stopped, server_id, secrets, cpus)
SELECT id, project_id, name, kind, image, replicas, created_at, updated_at, port, domain, env, memory_mb, health_path, pre_deploy, current_deployment_id, stopped, server_id, secrets, cpus
FROM services;
DROP TABLE services;
ALTER TABLE services_new RENAME TO services;
CREATE INDEX services_project_id ON services (project_id);
CREATE UNIQUE INDEX services_domain ON services (domain) WHERE domain <> '';
COMMIT;
PRAGMA foreign_keys = ON;

-- +goose Down
-- Foreign keys still on: their deployments and schedules go with them.
DELETE FROM services WHERE kind = 'redis';
PRAGMA foreign_keys = OFF;
BEGIN;
CREATE TABLE services_old (
    id                    TEXT PRIMARY KEY,
    project_id            TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name                  TEXT NOT NULL,
    kind                  TEXT NOT NULL CHECK (kind IN ('app', 'postgres')),
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
    UNIQUE (project_id, name)
) STRICT;
INSERT INTO services_old (id, project_id, name, kind, image, replicas, created_at, updated_at, port, domain, env, memory_mb, health_path, pre_deploy, current_deployment_id, stopped, server_id, secrets, cpus)
SELECT id, project_id, name, kind, image, replicas, created_at, updated_at, port, domain, env, memory_mb, health_path, pre_deploy, current_deployment_id, stopped, server_id, secrets, cpus
FROM services;
DROP TABLE services;
ALTER TABLE services_old RENAME TO services;
CREATE INDEX services_project_id ON services (project_id);
CREATE UNIQUE INDEX services_domain ON services (domain) WHERE domain <> '';
COMMIT;
PRAGMA foreign_keys = ON;
