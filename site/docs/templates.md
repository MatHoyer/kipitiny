---
title: App templates
description: One-click apps shipped with kipitiny, what installing one does, and how a template is written
order: 15
---

> A template installs a common self-hosted app in one step: you answer a few questions, see what will be created, and kipitiny creates and deploys it. Afterwards they're ordinary services: edit, back up or delete them like any other.

## Installing one

- **Projects › From a template** installs into a new project (named after the app by default, on the server you pick).
- **Project page › Add an app** installs into that project, next to its other services. Not for projects linked to [git](/docs/compose#git): add the services to the file instead.
- **MCP:** `list_templates` lists them with their inputs; `install_template` installs one into a project, created if missing (`dry_run` shows the plan only).

Each template asks only what it can't guess (a domain, a key from another app). Secrets it needs are generated and stored as secrets. **Preview** shows the services to create; nothing exists until **Install and deploy**. Installing never deletes or changes a service that already exists: a name already taken in the project is refused.

## Available templates

<!-- templates -->

## Writing a template

A template is a folder in `internal/templates/files/`, built into kipitiny:

```
internal/templates/files/beszel-hub/
├── compose.yaml   the template
└── logo.svg       optional: the logo it brings
```

`compose.yaml` is a [compose file](/docs/compose) with a top-level `x-template` block (which Docker Compose and kipitiny's compose import ignore):

```yaml
x-template:
  title: Beszel hub
  description: Lightweight server monitoring with history, Docker stats and alerts.
  website: https://beszel.dev
  docs: https://beszel.dev/guide/getting-started
  icon: beszel
  category: monitoring
  tags: [metrics, docker, alerts]
  notes: |
    The first visit creates the admin account.

    Its data is in a volume: back it up from the service's **Backups** tab.
  logo:
    label: Beszel
    color: "#747BFF"
    images: [beszel, beszel-agent]
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

- The folder name is the template's id; `name` is the default project name.
- `notes` is markdown shown after the description, in the install dialog and in the list above: what to do after installing, ports to open, caveats. Paragraphs, lists, `**bold**`, `` `code` `` and `[links](…)` only, no headings; a link to `/docs/<page>` opens that page of the docs of the running version.
- `category` groups the gallery: `monitoring`, `analytics`, `automation`, `development`, `storage`, `media`, `communication`, `productivity`, `security` or `games`. `tags` are extra words its search matches.
- Each input becomes the variable `${NAME}` of the file (the `.env` of a compose import). A `${NAME}` that is a whole `environment` value is stored as a secret, unless `x-kipitiny.secrets` lists the secrets.
- Input fields: `name` (UPPER_CASE), `label`, `type` (`text`, `secret`, `domain`, `url`, `select` with its `options`, or `checkbox`, whose value is `true` or `false` and which `required` makes mandatory), `help` (its `https://` URLs become links), `placeholder`, `default`, `required`, and `generate` (a number of random bytes: the value is generated, not asked). Inputs work anywhere compose variables do (`ports`, `mem_limit`…) and in `x-kipitiny.domain`.

### Logos

`icon` names the logo shown for the template and for the services it creates. Either it's one the UI draws itself (`postgres`, `redis`, `nginx`, `node`), or one template brings it: a `logo.svg` next to its `compose.yaml`, and `x-template.logo` with its `label`, its brand `color` (`#rrggbb`, which tints its tile) and the `images` whose base name it also stands for (`ghcr.io/henrygd/beszel-agent:1` → `beszel-agent`), so services deployed without the template get it too. Several templates can share a logo (the Beszel agent uses the hub's); only one brings it.

The SVG is a plain drawing: one `<svg>` element with `xmlns`, at most 16 KB, no scripts, event handlers, `foreignObject` or links outside the file. The UI shows it as an image.

### Checks and updates

- A test checks that every template parses, that it uses exactly the variables it declares, and that its logo exists.
- A smoke test installs every template on a throwaway Docker, with its required inputs filled in, and checks that each service deploys and stays up. CI runs it on pull requests that change a template, and weekly. Locally, against a Docker daemon without kipitiny on it (a `docker:dind` container works): `KIPITINY_TEST_TEMPLATES=1 go test ./internal/core/ -run TemplatesDocker`.
- After adding or changing one, run `go generate ./internal/templates`: the demo on this website and the list above come from the file it generates, and a test fails while that file is stale.
- Pin images by version. Dependabot opens a pull request when a newer one is out (the Beszel images move together); CI checks the template still parses and runs. Apps already installed keep their version.
