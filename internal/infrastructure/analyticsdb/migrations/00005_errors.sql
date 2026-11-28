-- Error tracking: errors sent by Sentry SDKs, grouped into issues.

-- +goose Up
-- Apps the manager turned error tracking on for: id is the DSN's project
-- (SDKs want a number), key its public key.
CREATE TABLE error_projects (
    id  INTEGER PRIMARY KEY,
    key TEXT NOT NULL
) STRICT;

-- Events with the same fingerprint. status: open, resolved or ignored;
-- regressed_at is when a resolved one came back.
CREATE TABLE error_issues (
    id            INTEGER PRIMARY KEY,
    project       INTEGER NOT NULL,
    fingerprint   TEXT NOT NULL,
    title         TEXT NOT NULL,
    culprit       TEXT NOT NULL,
    level         TEXT NOT NULL,
    platform      TEXT NOT NULL,
    status        TEXT NOT NULL,
    first_seen    INTEGER NOT NULL,
    last_seen     INTEGER NOT NULL,
    events        INTEGER NOT NULL,
    users         INTEGER NOT NULL,
    first_release TEXT NOT NULL,
    last_release  TEXT NOT NULL,
    resolved_at   INTEGER NOT NULL,
    regressed_at  INTEGER NOT NULL,
    UNIQUE (project, fingerprint)
) STRICT;
CREATE INDEX error_issues_project_seen ON error_issues (project, last_seen);

-- The distinct users of an issue (a hash of what the SDK sent), counted in
-- error_issues.users.
CREATE TABLE error_users (
    issue INTEGER NOT NULL,
    user  TEXT NOT NULL,
    PRIMARY KEY (issue, user)
) STRICT;

-- Events per issue, release and minute (unix seconds): charts, and how a
-- deploy compares with the one before.
CREATE TABLE error_counts (
    issue   INTEGER NOT NULL,
    release TEXT NOT NULL,
    minute  INTEGER NOT NULL,
    n       INTEGER NOT NULL,
    PRIMARY KEY (issue, release, minute)
) STRICT;
CREATE INDEX error_counts_minute ON error_counts (minute);

-- The latest events of each issue (ErrorEvent as JSON), id a ULID.
CREATE TABLE error_events (
    id         TEXT PRIMARY KEY,
    issue      INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    data       TEXT NOT NULL
) STRICT;
CREATE INDEX error_events_issue ON error_events (issue, id);

-- +goose Down
DROP TABLE error_events;
DROP TABLE error_counts;
DROP TABLE error_users;
DROP TABLE error_issues;
DROP TABLE error_projects;
