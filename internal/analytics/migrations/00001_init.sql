-- Analytics events, in the analytics server's own SQLite file. Times are
-- unix seconds (INTEGER): stats bucket them with integer math, which SQLite
-- and Postgres share.

-- +goose Up
-- Hostnames the manager tracks; events for any other are dropped.
CREATE TABLE sites (
    hostname TEXT PRIMARY KEY
) STRICT;

-- The day's salt of visitor hashes. Kept so a restart doesn't count everyone
-- again; older ones are deleted, so a visitor can't be followed across days.
CREATE TABLE salts (
    day  INTEGER PRIMARY KEY,
    salt BLOB NOT NULL
) STRICT;

-- A visit ends after 30 minutes without an event. pageviews 1 is a bounce.
CREATE TABLE visits (
    id         TEXT PRIMARY KEY,
    hostname   TEXT NOT NULL,
    visitor    TEXT NOT NULL,
    started_at INTEGER NOT NULL,
    last_at    INTEGER NOT NULL,
    pageviews  INTEGER NOT NULL,
    entry      TEXT NOT NULL,
    exit       TEXT NOT NULL
) STRICT;
CREATE INDEX visits_host_started ON visits (hostname, started_at);
CREATE INDEX visits_visitor ON visits (visitor, last_at);

-- name is empty for a pageview, else a custom event with props (JSON).
CREATE TABLE events (
    id           TEXT PRIMARY KEY,
    hostname     TEXT NOT NULL,
    created_at   INTEGER NOT NULL,
    visitor      TEXT NOT NULL,
    visit_id     TEXT NOT NULL,
    name         TEXT NOT NULL,
    path         TEXT NOT NULL,
    query        TEXT NOT NULL,
    title        TEXT NOT NULL,
    referrer     TEXT NOT NULL,
    utm_source   TEXT NOT NULL,
    utm_medium   TEXT NOT NULL,
    utm_campaign TEXT NOT NULL,
    utm_term     TEXT NOT NULL,
    utm_content  TEXT NOT NULL,
    browser      TEXT NOT NULL,
    os           TEXT NOT NULL,
    device       TEXT NOT NULL,
    screen       TEXT NOT NULL,
    language     TEXT NOT NULL,
    country      TEXT NOT NULL,
    props        TEXT NOT NULL
) STRICT;
CREATE INDEX events_host_created ON events (hostname, created_at);

-- +goose Down
DROP TABLE events;
DROP TABLE visits;
DROP TABLE salts;
DROP TABLE sites;
