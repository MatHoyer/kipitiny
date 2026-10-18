-- Visitor profiles: a visit's journey, and who the app said the visitor is.

-- +goose Up
-- identity is the id an app passed to kipitiny.identify (its own user),
-- identity_props its props (JSON).
ALTER TABLE visits ADD COLUMN identity TEXT NOT NULL DEFAULT '';
ALTER TABLE visits ADD COLUMN identity_props TEXT NOT NULL DEFAULT '';
CREATE INDEX events_visit ON events (visit_id, created_at);

-- +goose Down
DROP INDEX events_visit;
ALTER TABLE visits DROP COLUMN identity_props;
ALTER TABLE visits DROP COLUMN identity;
