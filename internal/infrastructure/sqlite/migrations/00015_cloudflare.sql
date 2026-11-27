-- +goose Up
-- Proxied through Cloudflare (orange cloud) when the manager manages its DNS.
ALTER TABLE domains ADD COLUMN proxied INTEGER NOT NULL DEFAULT 0;
-- IPv4 address DNS records point at for apps on this server.
ALTER TABLE servers ADD COLUMN public_ip TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE servers DROP COLUMN public_ip;
ALTER TABLE domains DROP COLUMN proxied;
