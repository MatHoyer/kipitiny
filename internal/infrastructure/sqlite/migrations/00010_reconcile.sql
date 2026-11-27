-- +goose Up
-- Desired run state: set by Stop, cleared by Start/Restart/Deploy. The
-- reconciler never restarts a service the user stopped.
ALTER TABLE services ADD COLUMN stopped INTEGER NOT NULL DEFAULT 0;
-- Snapshot of the service as deployed, so replicas the reconciler recreates
-- match the running version even if settings changed since.
ALTER TABLE deployments ADD COLUMN config TEXT NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE deployments DROP COLUMN config;
ALTER TABLE services DROP COLUMN stopped;
