-- Pixels and tracked links. Their hits are events of the pseudo-host
-- "<kind>:<id>", so stats read them like a site's.

-- +goose Up
-- slug is in the URL (/p/<slug>.gif, /l/<slug>): random, or chosen.
-- target is a link's destination, empty for a pixel.
CREATE TABLE trackers (
    id         TEXT PRIMARY KEY,
    kind       TEXT NOT NULL,
    slug       TEXT NOT NULL UNIQUE,
    name       TEXT NOT NULL,
    target     TEXT NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE trackers;
