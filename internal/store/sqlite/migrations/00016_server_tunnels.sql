-- +goose Up
-- Cloudflare tunnel token of a server; empty means public ports 80/443 (for
-- the manager's own server, KIPITINY_CLOUDFLARE_TUNNEL_TOKEN still applies).
ALTER TABLE servers ADD COLUMN tunnel_token TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE servers DROP COLUMN tunnel_token;
