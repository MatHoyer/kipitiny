-- +goose Up
-- Issues that opened or reopened, in order, for the manager's alerts: kind
-- is new or regression. Kept 7 days.
CREATE TABLE error_changes (
    seq     INTEGER PRIMARY KEY,
    project INTEGER NOT NULL,
    issue   INTEGER NOT NULL,
    kind    TEXT NOT NULL,
    at      INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE error_changes;
