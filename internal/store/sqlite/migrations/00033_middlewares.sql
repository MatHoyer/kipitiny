-- +goose Up
-- Traefik middlewares on an app's router, as JSON (see store.Middlewares).
ALTER TABLE services ADD COLUMN middlewares TEXT NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE services DROP COLUMN middlewares;
