-- +goose Up
-- Where an invite was emailed; empty for a link copied by hand.
ALTER TABLE invites ADD COLUMN email TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE invites DROP COLUMN email;
