-- Uptime checks: the manager requests a service's public URL on an interval.

-- +goose Up
-- One check per service. expected_status 0 accepts any status below 400.
-- down is the state last notified, since changed_at.
CREATE TABLE uptime_checks (
    service_id      TEXT PRIMARY KEY REFERENCES services (id) ON DELETE CASCADE,
    path            TEXT NOT NULL DEFAULT '/',
    interval_sec    INTEGER NOT NULL,
    timeout_sec     INTEGER NOT NULL,
    expected_status INTEGER NOT NULL DEFAULT 0,
    enabled         INTEGER NOT NULL DEFAULT 1,
    down            INTEGER NOT NULL DEFAULT 0,
    changed_at      TEXT,
    created_at      TEXT NOT NULL
) STRICT;

-- Results, counted per hour: enough for uptime percentages and response
-- times without keeping every check. latency_ms sums the successful ones.
CREATE TABLE uptime_hours (
    service_id TEXT NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    hour       TEXT NOT NULL,
    checks     INTEGER NOT NULL,
    failures   INTEGER NOT NULL,
    latency_ms INTEGER NOT NULL,
    PRIMARY KEY (service_id, hour)
) STRICT;

-- +goose Down
DROP TABLE uptime_hours;
DROP TABLE uptime_checks;
