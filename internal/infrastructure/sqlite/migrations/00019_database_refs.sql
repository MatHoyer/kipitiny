-- +goose Up
-- Apps reach databases through env references ({{ db.NAME.URL }}) instead
-- of a single link: turn each link into DATABASE_URL, unless the app set its
-- own (it won over the link).
UPDATE services
SET env = json_set(env, '$.DATABASE_URL',
        '{{ db.' || (SELECT d.name FROM services d WHERE d.id = services.database_id) || '.URL }}')
WHERE database_id <> ''
  AND json_extract(env, '$.DATABASE_URL') IS NULL
  AND EXISTS (SELECT 1 FROM services d WHERE d.id = services.database_id);
ALTER TABLE services DROP COLUMN database_id;

-- +goose Down
ALTER TABLE services ADD COLUMN database_id TEXT NOT NULL DEFAULT '';
