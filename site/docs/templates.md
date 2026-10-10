---
title: App templates
description: One-click apps shipped with kipitiny, what installing one does, and how a template is written
order: 14
---

> A template installs a common self-hosted app in one step: you answer a few questions, see what will be created, and kipitiny creates and deploys it. Afterwards they're ordinary services: edit, back up or delete them like any other.

## Installing one

- **Projects › From a template** installs into a new project (named after the app by default, on the server you pick).
- **Project page › Add an app** installs into that project, next to its other services. Not for projects linked to [git](/docs/compose#git): add the services to the file instead.
- **MCP:** `list_templates` lists them with their inputs; `install_template` installs one into a project, created if missing (`dry_run` shows the plan only).

Each template asks only what it can't guess (a domain, a key from another app). Secrets it needs are generated and stored as secrets. **Preview** shows the services to create; nothing exists until **Install and deploy**. Installing never deletes or changes a service that already exists: a name already taken in the project is refused.

## Available templates

### Beszel hub

[Beszel](https://beszel.dev)'s dashboard: server and container metrics, history and alerts.

- Asks for the **domain** it's served on. The first visit creates the admin account.
- Its data (users, systems, history) is in a volume: back it up from the service's **Backups** tab.

### Beszel agent

Reports the server it runs on, and its containers, to a Beszel hub. It runs with the server's network and Docker socket (see [Host access](/docs/compose#host-access)), so only on the server it watches: install it once per server, in a project on that server.

1. In the hub, **Add System**: copy its **public key**, and give the system the server's address and port `45876`.
2. Install the agent with that key. The hub connects to it on port 45876: open it in the server's firewall to the hub only.
3. Or give the agent a **token** and the **hub URL** (both from the hub): it connects to the hub itself, and no port needs to be open.

## Writing a template

A template is a [compose file](/docs/compose) in `internal/templates/files/<id>.yaml`, built into kipitiny, with a top-level `x-template` block (which Docker Compose and kipitiny's compose import ignore):

```yaml
x-template:
  title: Beszel hub
  description: Lightweight server monitoring with history, Docker stats and alerts.
  website: https://beszel.dev
  docs: https://beszel.dev/guide/getting-started
  icon: beszel
  inputs:
    - name: DOMAIN
      label: Domain
      type: domain
      required: true
      help: Where the dashboard is served.
name: beszel
services:
  hub:
    image: henrygd/beszel:0.21.0
    expose: ["8090"]
    environment:
      APP_URL: https://${DOMAIN}
    volumes:
      - data:/beszel_data
    x-kipitiny:
      domain: ${DOMAIN}
volumes:
  data: {}
```

- `name` is the default project name; the file's id (its file name) is the template's.
- Each input becomes the variable `${NAME}` of the file (the `.env` of a compose import). A `${NAME}` that is a whole `environment` value is stored as a secret, unless `x-kipitiny.secrets` lists the secrets.
- Input fields: `name` (UPPER_CASE), `label`, `type` (`text`, `secret`, `domain` or `url`), `help`, `placeholder`, `default`, `required`, and `generate` (a number of random bytes: the value is generated, not asked).
- Pin images by version, and update a template by bumping its tag. Apps already installed keep theirs.
- A test checks that every template parses, and that it uses exactly the variables it declares. The logo named by `icon` must exist in the UI (`web/src/components/service-icon.tsx`).
