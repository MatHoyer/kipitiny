-- Scheduled jobs: a command run on a cron schedule in a one-off container of
-- an app's image.

-- +goose Up
CREATE TABLE jobs (
    id          TEXT PRIMARY KEY,
    service_id  TEXT NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    command     TEXT NOT NULL,
    cron        TEXT NOT NULL,
    timeout_sec INTEGER NOT NULL,
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT NOT NULL,
    UNIQUE (service_id, name)
) STRICT;

-- Runs; their logs are files in the data directory. exit_code is NULL when
-- the command never ran or was killed.
CREATE TABLE job_runs (
    id           TEXT PRIMARY KEY,
    job_id       TEXT NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    status       TEXT NOT NULL,
    exit_code    INTEGER,
    triggered_by TEXT NOT NULL DEFAULT '',
    error        TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    finished_at  TEXT
) STRICT;

CREATE INDEX job_runs_job_id ON job_runs (job_id, created_at);

-- +goose Down
DROP TABLE job_runs;
DROP TABLE jobs;
