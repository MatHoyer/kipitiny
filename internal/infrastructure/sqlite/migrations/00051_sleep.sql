-- +goose Up
-- Sleep mode: an app with no request for sleep_after_seconds (0 = never) is
-- stopped by the manager and started again on the next request. sleeping is
-- set while it is; a start, stop or deploy clears it.
ALTER TABLE services ADD COLUMN sleep_after_seconds INTEGER NOT NULL DEFAULT 0;
ALTER TABLE services ADD COLUMN sleeping INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE services DROP COLUMN sleeping;
ALTER TABLE services DROP COLUMN sleep_after_seconds;
