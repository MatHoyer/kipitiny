-- +goose Up
ALTER TABLE services ADD COLUMN port INTEGER NOT NULL DEFAULT 0;
ALTER TABLE services ADD COLUMN domain TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN env TEXT NOT NULL DEFAULT '{}';

CREATE UNIQUE INDEX services_domain ON services (domain) WHERE domain <> '';

CREATE TABLE deployments (
    id          TEXT PRIMARY KEY,
    service_id  TEXT NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    status      TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
    image       TEXT NOT NULL,
    error       TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    finished_at TEXT
) STRICT;

CREATE INDEX deployments_service_id ON deployments (service_id, created_at);

-- +goose Down
DROP TABLE deployments;
DROP INDEX services_domain;
ALTER TABLE services DROP COLUMN env;
ALTER TABLE services DROP COLUMN domain;
ALTER TABLE services DROP COLUMN port;
