-- +goose Up
-- An app's own healthcheck, from compose's healthcheck key, as JSON (see
-- store.Healthcheck); '{}' keeps the image's or the injected probe.
ALTER TABLE services ADD COLUMN healthcheck TEXT NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE services DROP COLUMN healthcheck;
