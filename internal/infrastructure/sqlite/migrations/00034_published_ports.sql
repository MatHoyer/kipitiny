-- +goose Up
-- Host ports bound to an app's container, as JSON
-- [{"hostPort": ..., "containerPort": ..., "protocol": "tcp"|"udp"}].
ALTER TABLE services ADD COLUMN published_ports TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE services DROP COLUMN published_ports;
