-- +goose Up
-- Container CPU limit in CPUs (0.5 = half a core); 0 means unlimited.
ALTER TABLE services ADD COLUMN cpus REAL NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE services DROP COLUMN cpus;
