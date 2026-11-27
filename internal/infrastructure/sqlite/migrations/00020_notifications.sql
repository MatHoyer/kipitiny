-- +goose Up
-- Where notifications go. config holds the kind's settings (a Discord
-- webhook URL, later SMTP or the like); events lists the event types sent.
CREATE TABLE notification_channels (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    kind       TEXT NOT NULL,
    config     TEXT NOT NULL DEFAULT '{}',
    events     TEXT NOT NULL DEFAULT '[]',
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL
) STRICT;

-- +goose Down
DROP TABLE notification_channels;
