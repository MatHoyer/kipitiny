---
title: Configuration
description: Every environment variable the manager reads
order: 11
---

| Env var              | Default | |
|----------------------|---------|---|
| `KIPITINY_ADDR`      | `:3000` | HTTP listen address |
| `KIPITINY_DOMAIN`    | — | Serve the UI/API over HTTPS on this domain, through Traefik |
| `KIPITINY_IMAGE`     | `ghcr.io/mathoyer/kipitiny` | Where new versions are looked up and pulled |
| `KIPITINY_UPDATE_CHECK` | `on` | `off` stops looking for new versions |
| `KIPITINY_DATA_DIR`  | `/data` | SQLite DB, deploy logs, local backups. Local disk only. |
| `KIPITINY_LOG_LEVEL` | `info`  | `debug`, `info`, `warn`, `error` |
| `KIPITINY_SETUP_TOKEN` | random | Fix the first-run setup token instead of generating one |
| `KIPITINY_TRUSTED_PROXIES` | — | Comma-separated CIDRs/IPs allowed to set `X-Forwarded-For` (your own proxy). Loopback and the managed Traefik are always trusted |
| `KIPITINY_TRAEFIK`   | `true`  | Run and maintain the Traefik container (`false` to bring your own) |
| `KIPITINY_TRAEFIK_IMAGE` | `traefik:v3.7` | |
| `KIPITINY_HTTP_PORT` / `KIPITINY_HTTPS_PORT` | `80` / `443` | Host ports Traefik binds |
| `KIPITINY_ACME_EMAIL` | — | Let's Encrypt account email (optional) |
| `KIPITINY_CLOUDFLARE_TUNNEL_TOKEN` | — | Receive traffic through a Cloudflare Tunnel instead of ports 80/443 |
| `KIPITINY_CLOUDFLARED_IMAGE` | `cloudflare/cloudflared:2026.9.3` | |
| `KIPITINY_MANAGER_BACKUP_CRON` | `@daily` | First-start schedule for snapshots of the manager's state (`off` for none) |
| `KIPITINY_MANAGER_BACKUP_TARGET` | `local` | Storage ID for that schedule |
| `KIPITINY_MANAGER_BACKUP_KEEP` | `14` | Snapshots that schedule keeps |
| `KIPITINY_DOCKER_SOCKET` | `/var/run/docker.sock` | Host socket path mounted into Traefik |
| `DOCKER_HOST`        | socket  | Standard Docker client env vars apply |

Traefik is (re)created on startup whenever its configuration changes; it keeps
running across manager restarts. HTTP redirects to HTTPS, certificates come from
Let's Encrypt (TLS-ALPN challenge, so port 443 must be reachable publicly).
