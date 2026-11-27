-- +goose Up
-- Where the service runs: "" everywhere, "only" in previews (never deployed
-- in its project), "skip" everywhere but previews.
ALTER TABLE services ADD COLUMN preview TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE services DROP COLUMN preview;
