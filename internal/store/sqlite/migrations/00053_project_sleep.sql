-- +goose Up
-- Sleep mode moves from apps to projects: a project with no request for
-- sleep_after_seconds (0 = never) has every service stopped, databases too,
-- and started again on the next request. Apps' own settings are dropped
-- (UPGRADE.md says so); previews take theirs from the parent's preview
-- settings (30 minutes by default). services.sleeping stays: set on every
-- service the project's sleep stopped.
ALTER TABLE projects ADD COLUMN sleep_after_seconds INTEGER NOT NULL DEFAULT 0;
UPDATE projects SET sleep_after_seconds = COALESCE(
    (SELECT NULLIF(ps.sleep_after_seconds, 0) FROM preview_settings ps WHERE ps.project_id = projects.parent_id), 1800)
WHERE parent_id != '';
ALTER TABLE services DROP COLUMN sleep_after_seconds;
-- Asleep apps start again: the reconciler recreates their containers.
UPDATE services SET sleeping = 0;

-- +goose Down
ALTER TABLE services ADD COLUMN sleep_after_seconds INTEGER NOT NULL DEFAULT 0;
ALTER TABLE projects DROP COLUMN sleep_after_seconds;
