-- Google Drive (rclone) and Proton Drive (proton-drive CLI) targets; config
-- holds their credentials as JSON: rclone options, or the CLI's session files.
-- SQLite can't change a CHECK constraint, so the table is rebuilt (with
-- foreign keys off: schedules and backups reference it).

-- +goose NO TRANSACTION
-- +goose Up
PRAGMA foreign_keys = OFF;
BEGIN;
CREATE TABLE backup_targets_new (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    kind          TEXT NOT NULL CHECK (kind IN ('local', 's3', 'gdrive', 'protondrive')),
    endpoint      TEXT NOT NULL DEFAULT '',
    region        TEXT NOT NULL DEFAULT '',
    bucket        TEXT NOT NULL DEFAULT '',
    prefix        TEXT NOT NULL DEFAULT '',
    access_key    TEXT NOT NULL DEFAULT '',
    secret_key    TEXT NOT NULL DEFAULT '',
    use_ssl       INTEGER NOT NULL DEFAULT 1,
    created_at    TEXT NOT NULL,
    age_recipient TEXT NOT NULL DEFAULT '',
    age_identity  TEXT NOT NULL DEFAULT '',
    config        TEXT NOT NULL DEFAULT '{}'
) STRICT;
INSERT INTO backup_targets_new (id, name, kind, endpoint, region, bucket, prefix, access_key, secret_key, use_ssl, created_at, age_recipient, age_identity)
SELECT id, name, kind, endpoint, region, bucket, prefix, access_key, secret_key, use_ssl, created_at, age_recipient, age_identity
FROM backup_targets;
DROP TABLE backup_targets;
ALTER TABLE backup_targets_new RENAME TO backup_targets;
COMMIT;
PRAGMA foreign_keys = ON;

-- +goose Down
PRAGMA foreign_keys = OFF;
BEGIN;
CREATE TABLE backup_targets_old (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    kind          TEXT NOT NULL CHECK (kind IN ('local', 's3')),
    endpoint      TEXT NOT NULL DEFAULT '',
    region        TEXT NOT NULL DEFAULT '',
    bucket        TEXT NOT NULL DEFAULT '',
    prefix        TEXT NOT NULL DEFAULT '',
    access_key    TEXT NOT NULL DEFAULT '',
    secret_key    TEXT NOT NULL DEFAULT '',
    use_ssl       INTEGER NOT NULL DEFAULT 1,
    created_at    TEXT NOT NULL,
    age_recipient TEXT NOT NULL DEFAULT '',
    age_identity  TEXT NOT NULL DEFAULT ''
) STRICT;
INSERT INTO backup_targets_old (id, name, kind, endpoint, region, bucket, prefix, access_key, secret_key, use_ssl, created_at, age_recipient, age_identity)
SELECT id, name, kind, endpoint, region, bucket, prefix, access_key, secret_key, use_ssl, created_at, age_recipient, age_identity
FROM backup_targets WHERE kind IN ('local', 's3');
DROP TABLE backup_targets;
ALTER TABLE backup_targets_old RENAME TO backup_targets;
COMMIT;
PRAGMA foreign_keys = ON;
