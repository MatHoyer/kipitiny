-- Browsers that receive push notifications (Web Push), one per device.

-- +goose Up
-- p256dh and auth are the browser's keys the payload is encrypted with.
CREATE TABLE push_subscriptions (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    endpoint   TEXT NOT NULL UNIQUE,
    p256dh     TEXT NOT NULL,
    auth       TEXT NOT NULL,
    created_at TEXT NOT NULL
) STRICT;

CREATE INDEX push_subscriptions_user_id ON push_subscriptions (user_id);

-- +goose Down
DROP TABLE push_subscriptions;
