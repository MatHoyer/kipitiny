-- Tags on projects and services (JSON arrays of names), and the colors
-- users picked for some tags; the others get one derived from their name.
-- +goose Up
ALTER TABLE projects ADD COLUMN tags TEXT NOT NULL DEFAULT '[]';
ALTER TABLE services ADD COLUMN tags TEXT NOT NULL DEFAULT '[]';
CREATE TABLE tag_colors (
    name       TEXT PRIMARY KEY,
    color      TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

-- +goose Down
DROP TABLE tag_colors;
ALTER TABLE services DROP COLUMN tags;
ALTER TABLE projects DROP COLUMN tags;
