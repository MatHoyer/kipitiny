-- +goose Up
-- How a database is anonymized for previews (JSON anonymize.Rules); NULL
-- has no rules, '{}' holds nothing personal.
ALTER TABLE services ADD COLUMN anonymize TEXT;

-- Seeding previews with a project's data: '' (off), anonymized or raw.
-- salt keys the anonymized values' hashes; previews never see it.
ALTER TABLE preview_settings ADD COLUMN seed_mode TEXT NOT NULL DEFAULT '';
ALTER TABLE preview_settings ADD COLUMN salt TEXT NOT NULL DEFAULT '';
UPDATE preview_settings SET seed_mode = 'raw' WHERE allow_seed = 1;
ALTER TABLE preview_settings DROP COLUMN allow_seed;

-- An anonymized copy of the backup source_id, made with the rules hashing
-- to rules_hash.
ALTER TABLE backups ADD COLUMN source_id TEXT NOT NULL DEFAULT '';
ALTER TABLE backups ADD COLUMN rules_hash TEXT NOT NULL DEFAULT '';
CREATE INDEX backups_source_id ON backups (source_id);

-- +goose Down
DROP INDEX backups_source_id;
ALTER TABLE backups DROP COLUMN rules_hash;
ALTER TABLE backups DROP COLUMN source_id;
ALTER TABLE preview_settings ADD COLUMN allow_seed INTEGER NOT NULL DEFAULT 0;
UPDATE preview_settings SET allow_seed = 1 WHERE seed_mode != '';
ALTER TABLE preview_settings DROP COLUMN salt;
ALTER TABLE preview_settings DROP COLUMN seed_mode;
ALTER TABLE services DROP COLUMN anonymize;
