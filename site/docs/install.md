---
title: Install
description: Run kipitiny on a server, create the admin account, recover access
order: 1
---

To set up a fresh server (firewall, SSH hardening, Docker, kipitiny), use the
Ansible playbook in [`deploy/ansible`](https://github.com/MatHoyer/kipitiny/tree/main/deploy/ansible). By hand, only
this repo's `docker-compose.yml` is needed on the server:

```sh
docker compose up -d   # http://localhost:3000
```

The manager controls the host Docker daemon through the mounted socket.
**Access to the socket is root on the host**, so the UI and API require a login.

On first start the manager logs a one-time **setup token**
(`docker compose logs manager | grep setup_token`); the UI asks for it to create
the admin account. Forgot the password?

```sh
docker compose exec -it manager /kipitiny reset-password admin
```

Two-factor authentication (authenticator app) and passkeys are set up from
**Account** in the user menu. Lost the authenticator and the recovery codes?

```sh
docker compose exec manager /kipitiny disable-2fa admin
```

Passkeys are bound to the domain the UI is opened at, and need HTTPS (or
`localhost`).

Next: [configuration](/docs/configuration) and [domains and HTTPS](/docs/domains).
