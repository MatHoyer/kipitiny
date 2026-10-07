# kipitiny compose reference

> A kipitiny project is described by a docker-compose file. You can write it by hand, keep it in git, have an LLM write it, or export it from any project. This page is the complete format: everything kipitiny reads, what each field does, and what it refuses.
>
> Served by every manager at `/llms.txt` (no sign-in) and in the UI under **Compose reference**. It always matches that manager's version.

## How a file is used

- **Export:** project page › **Compose** gives the project (or, on a service page, one service) as a file. Secret values are never in it: each one becomes a `${NAME}` variable. **Export with secrets** also downloads the `.env` holding their values.
- **Import:** project page › **Compose** › **Import** applies a file (and optionally a `.env`) after a preview. It creates the services the file adds and updates the ones that differ. It can also delete the apps the file doesn't list; databases are never deleted.
- **Git:** project page › **Git** links the project to a file in a repository. Every commit on the branch is applied, apps the file drops are deleted, and the services can no longer be edited any other way (see [Git](#git)).
- **MCP:** the `get_project_compose` and `apply_project_compose` tools do the same for AI agents; `get_compose_reference` returns this page.

The file stays a valid docker-compose file: kipitiny's own settings live in `x-kipitiny` blocks, which Docker Compose ignores.

## A complete example

```yaml
name: shop
services:
  web:
    image: ghcr.io/me/shop:1.4.2
    environment:
      DATABASE_URL: "{{ db.db.URL }}"
      REDIS_URL: "{{ db.cache.URL }}"
      REGION: "{{ project.REGION }}"
      LOG_LEVEL: info
      API_KEY: ${API_KEY}
    volumes:
      - uploads:/app/uploads
    deploy:
      replicas: 2
      resources:
        limits: {memory: 512M, cpus: "1"}
    stop_grace_period: 30s
    x-kipitiny:
      domain: shop.example.com
      port: 3000
      health_path: /healthz
      pre_deploy: ./bin/migrate up
      secrets: [API_KEY]
      middlewares:
        rate_limit: {average: 50, burst: 100}
        headers: {X-Frame-Options: DENY}
  db:
    image: postgres:17-alpine
    deploy:
      resources:
        limits: {memory: 1G}
  cache:
    image: redis:8-alpine
volumes:
  uploads: {}
x-kipitiny:
  variables:
    REGION: eu
```

`db` and `cache` become a managed PostgreSQL and Redis (official images are recognised), `web` connects to them through references, and `API_KEY` comes from the project variable `API_KEY` (or a `.env`).

## Services

Each key under `services` is a service. Its name is also its hostname inside the project: lowercase letters, digits and dashes, at most 40 characters.

### Compose keys kipitiny reads

| Key | Meaning |
|---|---|
| `image` | The image to run, pinned by tag (or digest). Required for apps. kipitiny never builds: build and push in CI. |
| `environment` | Env variables, as a mapping or a `KEY=value` list. Values may hold references (see [Values](#values-references-and-secrets)). A key without a value (`- KEY` or `KEY:`) means `${KEY}`. |
| `ports` | Host ports published straight to the container, for traffic that isn't HTTP: `"25565:25565"`, `"9000:9000/udp"`, or the long form `{target: 25565, published: 25565, protocol: tcp}`. One port per entry, no ranges. An app with published ports runs one replica. HTTP goes through `x-kipitiny.port` instead. |
| `volumes` | Named volumes: `name:/path` or `{type: volume, source: name, target: /path}`. Shared by the service's replicas, kept across deploys, backed up. At most 10. Names: lowercase letters, digits and dashes. |
| `deploy` | `replicas` (1–10; databases always 1) and `resources.limits.memory` / `resources.limits.cpus`. |
| `mem_limit` | Same as `deploy.resources.limits.memory` (`512m`, `1g`, bytes). |
| `cpus` | Same as `deploy.resources.limits.cpus` (cores, `0.5` = half a core; 0.01–256). |
| `stop_grace_period` | Time an app gets to exit after SIGTERM before it is killed (`30s`, `1m30s`; at most 10 minutes; default 10 s). Not for databases. |
| `build` | Ignored when `image` is set (with a warning); an error without one. |
| `x-kipitiny` | kipitiny's settings, below. |

Top level: `name` (the project name, informational), `services`, `volumes` (each volume's options are ignored: kipitiny creates local named volumes) and `x-kipitiny` (project variables).

### Ignored with a warning

kipitiny orders, networks, restarts and health-checks services itself, so these are dropped and reported: `container_name`, `depends_on`, `expose`, `healthcheck`, `hostname`, `labels`, `links`, `logging`, `networks`, `pull_policy`, `restart`, `init`, `extra_hosts`, `stop_signal`, and the top-level `version` and `networks`. Other `x-*` keys are ignored silently.

### Errors

Any other key is refused, because ignoring it would run something different from what the file says: for example `command`, `entrypoint`, `user`, `working_dir`, `env_file`, `secrets`, `configs`, `privileged`, `cap_add`, `devices`, `network_mode`, `profiles`, `extends`, `tmpfs`. Bind mounts (`./data:/data`, `/srv:/srv`) and anonymous volumes (`/data`) are refused too: use a named volume.

### `x-kipitiny` (per service)

| Field | Type | Applies to | Meaning |
|---|---|---|---|
| `kind` | `app` \| `postgres` \| `redis` | all | What the service is. Default: `postgres` or `redis` when the image is the official one (`postgres:17-alpine`, `redis:8`), else `app`. Set `kind: app` to run those images as plain containers. |
| `domain` | hostname | apps | Public hostname, served over HTTPS by Traefik. Needs `port` (unless the app only publishes ports and the domain is only for DNS). |
| `port` | 1–65535 | apps | Container port Traefik routes the domain to. |
| `health_path` | path | apps | HTTP path (e.g. `/healthz`) that must answer 2xx/3xx before a new replica takes traffic. Needs `port`. Without it: the image's healthcheck, else a stability wait. |
| `pre_deploy` | shell command | apps | Runs once (`sh -c`) in a one-off container before a rollout, e.g. migrations. A failure aborts the deploy. |
| `pre_backup` | shell command | apps | Runs in a running replica before each volume backup, e.g. to flush to disk. |
| `icon` | name | all | Logo the UI shows (e.g. `ghost`, `n8n`). Default: from the kind or image. |
| `secrets` | list of env keys | apps | Which `environment` entries are secrets (write-only in the UI and API, masked everywhere, exported only with secrets). |
| `password` | string, usually `${NAME}` | databases | The database's password at creation, to keep the credentials of a moved database. Empty generates one. It can't change afterwards. |
| `middlewares` | mapping | public apps | Applied by Traefik to every request, below. |

`middlewares`:

| Field | Meaning |
|---|---|
| `basic_auth` | Up to 20 users, each with a `name` and either a `hash` (bcrypt, e.g. from `htpasswd -nbB user pass`) or a `ref` (a password manager reference `{{ pass://Vault/Item/field }}`, hashed at deploy). Plain passwords are refused: they'd sit in the file. |
| `ip_allowlist` | Up to 50 IPs or CIDR ranges allowed to connect. |
| `rate_limit` | `average` requests per second per client IP, with bursts up to `burst` (defaults to `average`). |
| `headers` | Response headers to set (up to 20); an empty value removes the header. |

### Databases

`postgres` and `redis` services are managed: one replica, a data volume of their own, a generated password, private to the project, backed up and restore-tested.

- Only `image`, the memory and CPU limits, `icon` and `password` apply. `environment`, `volumes` and `ports` are ignored with a warning; `stop_grace_period`, `domain` and `middlewares` are errors.
- Postgres needs a major version in the tag (`postgres:17-alpine`). It can't change major version afterwards (that takes a dump and restore). Memory: at least 128 MB, default 512 MB.
- Redis memory: at least 32 MB, default 256 MB; Redis keeps its data within 75% of it.
- Apps connect through references: `{{ db.<service>.URL }}`, or the fields `HOST`, `PORT`, `USER`, `PASSWORD`, `DATABASE` (Postgres only).

## Values, references and secrets

Two syntaxes, resolved at different times:

| Syntax | Resolved | Meaning |
|---|---|---|
| `${NAME}` | When the file is applied | A compose variable, from the `.env` given with the file. `$$` is a literal `$`. `${NAME:-default}`, `${NAME-default}`, `${NAME:?message}` and `$NAME` work as in Docker Compose. |
| `{{ project.NAME }}` | At every deploy | The project's shared variable `NAME`. |
| `{{ db.SERVICE.FIELD }}` | At every deploy | A database's connection detail (see above). |
| `{{ pass://Vault/Item/field }}` | At every deploy | A secret read from a connected password manager; never stored by kipitiny. |

Compose variables work in every value except inside `x-kipitiny` blocks (bcrypt hashes are full of `$`). There, a value that is exactly `${NAME}` (`password`, a basic auth `hash`, a project variable) is taken from the `.env`.

When a `${NAME}` has no value:

- In `environment`, when it is the whole value (`API_KEY: ${API_KEY}`): it becomes `{{ project.API_KEY }}`, a reference to the project variable. **This is how a file in git stays free of secrets:** set the value once as a project variable (project › Environment), and the file only names it.
- An existing secret exported without its value (`API_KEY: ${WEB_API_KEY}` for the service `web`) keeps its current value.
- Anywhere else, the file is refused with the list of missing variables.

Exports name secret variables `<SERVICE>_<KEY>` (`WEB_API_KEY`), project secrets `PROJECT_<NAME>`, database passwords `<SERVICE>_PASSWORD` and basic auth hashes `<SERVICE>_BASIC_AUTH_<USER>`.

## Project variables

The top-level block sets the project's shared variables, which services read as `{{ project.NAME }}`:

```yaml
x-kipitiny:
  variables:
    REGION: eu
    STRIPE_KEY: ${PROJECT_STRIPE_KEY}
  secrets: [STRIPE_KEY]
```

| Field | Meaning |
|---|---|
| `variables` | Name → value. A variable the file doesn't list is kept, never removed. |
| `secrets` | Which variables are secrets. |

A variable whose value is a missing `${NAME}` keeps its current value; on a project that doesn't have it yet, the file is refused.

## Applying a file

1. The whole file is parsed and every service validated against the project as it will be (variables, databases, ports, domains). Any error stops everything: nothing has changed.
2. Project variables are set; services are created (databases first) and updated where something differs.
3. With pruning (always with git, an option on import), apps the file doesn't list are deleted with their volumes. Databases the file doesn't list are only reported, and kept until deleted by hand.
4. Created services, and services whose change needs it (anything but `replicas` and `icon`), are deployed: databases first, then apps once the databases are up. Scaling applies at once.

A service can't change kind (an app into a database): delete it first.

## Git

Project › **Git**: an HTTPS repository URL, a branch (default `main`), the file's path (default `compose.yaml`), and for a private repository an access token with read access to its contents. Linking shows what the first sync will change.

- The branch is checked every 5 minutes by default (60 s to 24 h, or never with auto sync off). A push webhook syncs at once: the URL and secret are on the Git tab. GitHub and Gitea sign with the secret, GitLab sends it as a token, anything else as `Authorization: Bearer <secret>`. **Sync now** applies the file by hand, **Preview** shows what it would change.
- The file owns the services. Creating, editing or deleting them any other way (UI, API, MCP) is refused; edit the file. A database the file dropped can still be deleted by hand. Project variables, backups, schedules and uptime checks stay editable: they are not in the file.
- CI can still deploy another tag of an app's image (`kipitiny deploy --tag`). The service keeps it until a commit changes that service's block in the file; the Git tab lists the services that run another image than the file's.
- A failed sync is notified (`git.sync.failed`) and retried; a sync that changed something can be notified too (`git.sync.succeeded`).
- Unlinking keeps the services as they are and makes them editable again.

## Moving a project to another kipitiny

1. On the old manager: project › **Compose** › **Export with secrets** (your password is asked again). Keep the zip safe: its `.env` holds every credential of the project.
2. On the new one: create the project, then **Compose** › **Import** with `compose.yaml` and `.env`.
3. Data moves with backups: back up the databases and volumes to a storage both managers reach, and restore them on the new services.
