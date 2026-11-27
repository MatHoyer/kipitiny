-- +goose Up
-- The maintenance page and mode move from apps to projects: one page for
-- every app of the project (JSON, see store.Maintenance). A project takes
-- the page of its first app (by name) that had one; maintenance mode is
-- off after the upgrade (UPGRADE.md says so).
ALTER TABLE projects ADD COLUMN maintenance TEXT NOT NULL DEFAULT '{}';
UPDATE projects SET maintenance = COALESCE((
    SELECT json_set(s.maintenance, '$.enabled', json('false')) FROM services s
    WHERE s.project_id = projects.id AND s.kind = 'app' AND (
        json_extract(s.maintenance, '$.title') IS NOT NULL OR json_extract(s.maintenance, '$.message') IS NOT NULL OR
        json_extract(s.maintenance, '$.html') IS NOT NULL OR json_extract(s.maintenance, '$.starting') IS NOT NULL OR
        json_extract(s.maintenance, '$.allowIps') IS NOT NULL)
    ORDER BY s.name LIMIT 1), '{}');
ALTER TABLE services DROP COLUMN maintenance;

-- +goose Down
ALTER TABLE services ADD COLUMN maintenance TEXT NOT NULL DEFAULT '{}';
ALTER TABLE projects DROP COLUMN maintenance;
