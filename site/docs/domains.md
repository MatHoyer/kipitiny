---
title: Domains and HTTPS
description: HTTPS for the manager, Cloudflare Tunnel and Cloudflare DNS
order: 8
---

## HTTPS for the manager

Set `KIPITINY_DOMAIN=kipitiny.example.com` (DNS pointing at the server) and the
manager's Traefik routes that domain to the UI with a Let's Encrypt certificate,
like any app. In a container, the manager attaches itself to the proxy network,
so port 3000 no longer needs to be published: drop it from the compose file or
bind it to `127.0.0.1` for SSH-tunnel access. Run on the host, the manager is
reached through `host.docker.internal`, so it must listen on the Docker bridge
(the default `:3000` does).

## Cloudflare Tunnel (no open ports)

To keep ports 80/443 closed, let traffic come in through a
[Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/):

1. In Cloudflare Zero Trust, create a tunnel (Networks → Tunnels, type
   *cloudflared*) and copy its token.
2. Either connect Cloudflare in *Settings* (below), which adds each service's
   hostname to the tunnel, or add public hostnames yourself: `example.com` and
   `*.example.com`, both with service **HTTPS** `kipitiny-traefik:443`, and
   under TLS enable **No TLS Verify** and **Match SNI to Host**. Check that DNS
   has a proxied `* CNAME <tunnel-id>.cfargotunnel.com` record, and add it if
   the dashboard didn't.
3. Paste the token in *Infrastructure › Servers › Network* (globe button), or start
   the manager with `KIPITINY_CLOUDFLARE_TUNNEL_TOKEN=<token>` for its own
   server (with `KIPITINY_DOMAIN`), and publish no port in the compose file.

The manager then runs `kipitiny-cloudflared` next to Traefik, and Traefik
publishes no ports and requests no Let's Encrypt certificates: Cloudflare
serves the public certificate, and Traefik's own certificate only protects the
hop from cloudflared. Service domains need no per-app setup as long as they
match a hostname of the tunnel. Cloudflare's free certificate covers one level
of subdomain (`app.example.com`, not `a.b.example.com`).

Each server can have its own tunnel (one token per server; create one tunnel
per server): the manager runs cloudflared on every server with a token, and
those servers publish no ports. With Cloudflare connected, each app's hostname
points at the tunnel of the server running it. Apps deployed before switching keep working, but redeploy them to drop
their Let's Encrypt labels (Traefik logs a "nonexistent certificate resolver"
error until then).

## Cloudflare DNS

List your domains in *Infrastructure › Domains & DNS* and connect Cloudflare
there with an API token (*My Profile › API Tokens*) allowed
**Zone › Zone › Read** and **Zone › DNS › Edit**, plus **Account › Cloudflare
Tunnel › Edit** with a tunnel. For every service domain in one of the token's
zones, the manager then keeps the DNS record in sync:

- an **A** record to the server's public IP (detected, or set under
  *Infrastructure › Servers*), proxied if the domain is marked **Proxied**;
- behind the tunnel, a proxied **CNAME** to it, and the tunnel's route to
  Traefik.

Records are created, updated and removed as services come and go; they carry
the comment `managed by kipitiny`, and records without it are never changed (a
service whose name already has one shows a DNS conflict). Certificates for these
domains come from Let's Encrypt through the Cloudflare DNS challenge, so they
work behind the proxy too; Traefik gets the token for that. Proxied domains
need the zone's SSL/TLS mode on **Full (strict)**. Services deployed before
connecting keep their certificate settings until their next deploy.

For local testing use a `*.localhost` domain and alternate ports, e.g.
`KIPITINY_HTTP_PORT=8081 KIPITINY_HTTPS_PORT=8443`, then
`curl -k https://app.localhost:8443` (Traefik serves its default self-signed cert).
