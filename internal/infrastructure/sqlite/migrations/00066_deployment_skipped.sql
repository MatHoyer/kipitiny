-- Deployments may be skipped (a compose dependency failed). SQLite can't
-- change a CHECK constraint, so the table is rebuilt; nothing references it.

-- +goose Up
CREATE TABLE deployments_new (
    id           TEXT PRIMARY KEY,
    service_id   TEXT NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    status       TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed', 'skipped')),
    image        TEXT NOT NULL,
    error        TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    finished_at  TEXT,
    config       TEXT NOT NULL DEFAULT '{}',
    git_commit   TEXT NOT NULL DEFAULT '',
    triggered_by TEXT NOT NULL DEFAULT ''
) STRICT;
INSERT INTO deployments_new SELECT * FROM deployments;
DROP TABLE deployments;
ALTER TABLE deployments_new RENAME TO deployments;
CREATE INDEX deployments_service_id ON deployments (service_id, created_at);

-- +goose Down
CREATE TABLE deployments_old (
    id           TEXT PRIMARY KEY,
    service_id   TEXT NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    status       TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
    image        TEXT NOT NULL,
    error        TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    finished_at  TEXT,
    config       TEXT NOT NULL DEFAULT '{}',
    git_commit   TEXT NOT NULL DEFAULT '',
    triggered_by TEXT NOT NULL DEFAULT ''
) STRICT;
INSERT INTO deployments_old SELECT id, service_id, CASE status WHEN 'skipped' THEN 'failed' ELSE status END, image, error, created_at, finished_at, config, git_commit, triggered_by FROM deployments;
DROP TABLE deployments;
ALTER TABLE deployments_old RENAME TO deployments;
CREATE INDEX deployments_service_id ON deployments (service_id, created_at);
