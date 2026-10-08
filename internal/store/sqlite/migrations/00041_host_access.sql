-- +goose Up
-- Host access for apps that monitor or manage the server (e.g. a Beszel or
-- node-exporter agent): host_network runs the app in the host's network
-- namespace; docker_socket mounts the host's Docker socket ('' none, 'ro'
-- read-only, 'rw').
ALTER TABLE services ADD COLUMN host_network INTEGER NOT NULL DEFAULT 0;
ALTER TABLE services ADD COLUMN docker_socket TEXT NOT NULL DEFAULT '' CHECK (docker_socket IN ('', 'ro', 'rw'));

-- +goose Down
ALTER TABLE services DROP COLUMN docker_socket;
ALTER TABLE services DROP COLUMN host_network;
