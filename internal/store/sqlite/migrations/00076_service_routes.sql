-- +goose Up
-- Paths of the service's domain routed to it, each with its own scheme and
-- port (store.Route); empty routes the whole domain.
ALTER TABLE services ADD COLUMN routes TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE services DROP COLUMN routes;
