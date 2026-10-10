---
title: Compose reference
description: The docker-compose format of a kipitiny project: keys, x-kipitiny settings, variables and secrets, git sync
order: 9
---

> A kipitiny project is described by a docker-compose file. You can write it by hand, keep it in git, have an LLM write it, or export it from any project. This page is the complete format: everything kipitiny reads, what each field does, and what it refuses.
>
> As markdown for LLMs at [`/docs/compose/llms.txt`](/docs/compose/llms.txt) (`/llms.txt` lists every page). The MCP endpoint points agents here.

## How a file is used

- **Export:** project page › **Compose** gives the project (or, on a service page, one service) as a file. Secret values are never in it: each one becomes a `${NAME}` variable. **Export with secrets** also downloads the `.env` holding their values.
- **Import:** project page › **Compose** › **Import** applies a file (and optionally a `.env`) after a preview. It creates the services the file adds and updates the ones that differ. It can also delete the apps the file doesn't list; databases are never deleted.
- **Git:** project page › **Git** links the project to a file in a repository. Every commit on the branch is applied, apps the file drops are deleted, and the services can no longer be edited any other way (see [Git](#git)).
- **MCP:** the `get_project_compose` and `apply_project_compose` tools do the same for AI agents; the server's instructions point them to these docs.

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
    expose: ["3000"]
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:3000/healthz"]
    volumes:
      - uploads:/app/uploads
    deploy:
      replicas: 2
      resources:
        limits: {memory: 512M, cpus: "1"}
    stop_grace_period: 30s
    x-kipitiny:
      domain: shop.example.com
      pre_deploy: ./bin/migrate up
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

`db` and `cache` become a managed PostgreSQL and Redis (official images are recognised), `web` connects to them through references, and `API_KEY` comes from the project variable `API_KEY` (or a `.env`, which makes it a secret). Plain compose keys say what compose already has words for (the port, the healthcheck); `x-kipitiny` holds only what compose has none for: the domain, the pre-deploy command, Traefik's middlewares.

A real one: this website runs on kipitiny from [`site/compose.yaml`](https://github.com/MatHoyer/kipitiny/blob/main/site/compose.yaml), synced from git. Each release pins its image tag there, in a commit pushed once the [site workflow](https://github.com/MatHoyer/kipitiny/blob/main/.github/workflows/site.yml) has published that image, so a sync never asks for an image that doesn't exist yet.

## Services

Each key under `services` is a service. Its name is also its hostname inside the project: lowercase letters, digits and dashes, at most 40 characters.

### Compose keys kipitiny reads

| Key | Meaning |
|---|---|
| `image` | The image to run, pinned by tag (or digest). Required for apps. kipitiny never builds: build and push in CI. |
| `environment` | Env variables, as a mapping or a `KEY=value` list. Values may hold references (see [Values](#values-references-and-secrets)). A key without a value (`- KEY` or `KEY:`) means `${KEY}`. |
| `expose` | The container port HTTP is routed to from the domain: `["3000"]`. With several, `x-kipitiny.port` picks one. Same as `x-kipitiny.port`. |
| `healthcheck` | Docker's healthcheck: `test` (a list starting with `CMD` or `CMD-SHELL`, or a string run by the shell), `interval`, `timeout`, `start_period`, `retries`, or `disable: true`. A new replica takes traffic once it passes. Without `start_period`, it is checked every second while starting. The command runs in the container, so the image needs it (`wget`, `curl`…); `x-kipitiny.health_path` works with any image. Not for databases. |
| `ports` | Host ports published straight to the container, for traffic that isn't HTTP: `"25565:25565"`, `"9000:9000/udp"`, or the long form `{target: 25565, published: 25565, protocol: tcp}`. One port per entry, no ranges. An app with published ports runs one replica. HTTP goes through `expose` (or `x-kipitiny.port`) instead. |
| `volumes` | Named volumes: `name:/path` or `{type: volume, source: name, target: /path}`. Shared by the service's replicas, kept across deploys, backed up, managed from the [Files](/docs/files) tab. At most 10. Names: lowercase letters, digits and dashes. The one bind mount allowed is the Docker socket, `/var/run/docker.sock:/var/run/docker.sock` (`:ro` for read-only); see [Host access](#host-access). |
| `network_mode` | Only `host`: the app runs in the server's network. See [Host access](#host-access). |
| `deploy` | `replicas` (1–10; databases always 1) and `resources.limits.memory` / `resources.limits.cpus`. |
| `mem_limit` | Same as `deploy.resources.limits.memory` (`512m`, `1g`, bytes). |
| `cpus` | Same as `deploy.resources.limits.cpus` (cores, `0.5` = half a core; 0.01–256). |
| `stop_grace_period` | Time an app gets to exit after SIGTERM before it is killed (`30s`, `1m30s`; at most 10 minutes; default 10 s). Not for databases. |
| `build` | Ignored when `image` is set (with a warning); an error without one. |
| `x-kipitiny` | kipitiny's settings, below. |

Top level: `name` (the project name, informational), `services`, `volumes` (each volume's options are ignored: kipitiny creates local named volumes) and `x-kipitiny` (project variables).

### Ignored with a warning

kipitiny orders, networks and restarts services itself, so these are dropped and reported: `container_name`, `depends_on`, `hostname`, `labels`, `links`, `logging`, `networks`, `pull_policy`, `restart`, `init`, `extra_hosts`, `stop_signal`, and the top-level `version` and `networks`. Other `x-*` keys are ignored silently. To connect services of different projects, use the [networks created by hand](/docs/services#networks), set outside the file.

### Errors

Any other key is refused, because ignoring it would run something different from what the file says: for example `command`, `entrypoint`, `user`, `working_dir`, `env_file`, `secrets`, `configs`, `privileged`, `cap_add`, `devices`, `profiles`, `extends`, `tmpfs`. Bind mounts (`./data:/data`, `/srv:/srv`, except the Docker socket) and anonymous volumes (`/data`) are refused too: use a named volume. So is any `network_mode` but `host`.

### Host access

Agents that report on or manage the server need what other apps don't: the host's network (to see its interfaces) and the Docker socket (to see its containers). An app can have both, from the file or its **Host access** settings. For example, a [Beszel](https://beszel.dev) agent:

```yaml
services:
  beszel-agent:
    image: henrygd/beszel-agent:latest
    network_mode: host
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    environment:
      LISTEN: 45876
      KEY: ${BESZEL_KEY}
```

- **`network_mode: host`:** the app shares the server's network and listens on its ports directly (open them in the firewall if needed). It has no domain or published ports, isn't on the project network (other services can't reach it by name), runs one replica, and deploys stop the old container before starting the new one.
- **The Docker socket** is mounted at `/var/run/docker.sock`, from the server's own socket. `:ro` only stops the app from replacing the socket file, not from using it: the app can still create, change and remove containers, so read-only gives no protection.
- Either one gives the app control of the server: only use images you trust. Databases can't have them.
- A service runs on its project's server: to monitor several servers, make one project per server. The Beszel hub itself is a normal app (`henrygd/beszel`, port 8090, a volume at `/beszel_data`). Both are also [one-click templates](/docs/templates).

### `x-kipitiny` (per service)

| Field | Type | Applies to | Meaning |
|---|---|---|---|
| `kind` | `app` \| `postgres` \| `redis` \| `mysql` \| `mariadb` \| `mongodb` | all | What the service is. Default: `postgres` or `redis` when the image is the official one (`postgres:17-alpine`, `redis:8`), else `app`. Set `kind: app` to run those images as plain containers. MySQL, MariaDB and MongoDB images stay apps unless `kind` says otherwise, so existing files that run them with their own `environment` and `volumes` keep working. |
| `domain` | hostname | apps | Public hostname, served over HTTPS by Traefik. Needs `port` (unless the app only publishes ports and the domain is only for DNS). |
| `port` | 1–65535 | apps | Container port Traefik routes the domain to. Usually written as `expose` instead. |
| `health_path` | path | apps | HTTP path (e.g. `/healthz`) that must answer 2xx/3xx before a new replica takes traffic, checked by a probe kipitiny injects, so the image needs no `curl`. Needs the port. Without it or a `healthcheck`: the image's healthcheck, else a stability wait. Not with `healthcheck`. |
| `pre_deploy` | shell command | apps | Runs once (`sh -c`) in a one-off container before a rollout, e.g. migrations. A failure aborts the deploy. |
| `pre_backup` | shell command | apps | Runs in a running replica before each volume backup, e.g. to flush to disk. |
| `icon` | name | all | Logo the UI shows (e.g. `ghost`, `n8n`). Default: from the kind or image. |
| `secrets` | list of env keys | apps | Which `environment` entries are secrets (write-only in the UI and API, masked everywhere, exported only with secrets). Without it, the values given as `${NAME}` are (see [Values](#values-references-and-secrets)). |
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

`postgres`, `redis`, `mysql`, `mariadb` and `mongodb` services are managed: one replica, a data volume of their own, a generated password, private to the project, backed up and restore-tested.

- Only `image`, the memory and CPU limits, `icon` and `password` apply. `environment`, `volumes` and `ports` are ignored with a warning; `stop_grace_period`, `domain` and `middlewares` are errors.
- Postgres needs a major version in the tag (`postgres:17-alpine`). It can't change major version afterwards (that takes a dump and restore). Memory: at least 128 MB, default 512 MB.
- Redis memory: at least 32 MB, default 256 MB; Redis keeps its data within 75% of it.
- MySQL (default `mysql:8.4`) and MariaDB (default `mariadb:11.8`) memory: at least 256 MB, default 512 MB; half of it goes to InnoDB's buffer pool.
- MongoDB (default `mongo:8.2`) memory: at least 512 MB, default 1 GB; WiredTiger's cache is sized from it like `mongod` does from a machine's memory.
- Apps connect through references: `{{ db.<service>.URL }}`, or the fields `HOST`, `PORT`, `USER`, `PASSWORD`, `DATABASE` (all but Redis).

## Values, references and secrets

Two syntaxes, resolved at different times:

| Syntax | Resolved | Meaning |
|---|---|---|
| `${NAME}` | When the file is applied | A compose variable, from the `.env` given with the file. `$$` is a literal `$`. `${NAME:-default}`, `${NAME-default}`, `${NAME:?message}` and `$NAME` work as in Docker Compose. |
| `{{ project.NAME }}` | At every deploy | The project's shared variable `NAME`. |
| `{{ db.SERVICE.FIELD }}` | At every deploy | A database's connection detail (see above). |
| `{{ pass://Vault/Item/field }}` | At every deploy | A secret read from a connected password manager; never stored by kipitiny. |

Compose variables work in every value except inside `x-kipitiny` blocks (bcrypt hashes are full of `$`). There, a value that is exactly `${NAME}` (`domain`, `password`, a basic auth `hash`, a project variable) is taken from the `.env`.

When a `${NAME}` has no value:

- In `environment`, when it is the whole value (`API_KEY: ${API_KEY}`): it becomes `{{ project.API_KEY }}`, a reference to the project variable. **This is how a file in git stays free of secrets:** set the value once as a project variable (project › Environment), and the file only names it.
- An existing secret exported without its value (`API_KEY: ${WEB_API_KEY}` for the service `web`) keeps its current value.
- Anywhere else, the file is refused with the list of missing variables.

A value given whole as `${NAME}` (or `- NAME` alone) and found in the `.env` is a **secret** (write-only, masked), unless `x-kipitiny.secrets` lists the secrets explicitly. One that became a project reference keeps the project variable's own flag.

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

Project › **Git**: the repository, a branch (default `main`) and the file's path (default `compose.yaml`). A public repository is just its HTTPS URL; a private one is picked from a [git provider](#git-providers). Linking shows what the first sync will change.

- The branch is checked every 5 minutes by default (60 s to 24 h, or never with auto sync off). A push webhook syncs at once. Through a GitHub provider whose app has webhooks, there's nothing to set up. Otherwise the URL and secret are on the Git tab: GitHub and Gitea sign with the secret, GitLab sends it as a token, anything else as `Authorization: Bearer <secret>`. **Sync now** applies the file by hand, **Preview** shows what it would change.
- The file owns the services. Creating, editing or deleting them any other way (UI, API, MCP) is refused; edit the file. A database the file dropped can still be deleted by hand. Project variables, backups, schedules and uptime checks stay editable: they are not in the file.
- CI can still deploy another tag of an app's image (`kipitiny deploy --tag`). The service keeps it until a commit changes that service's block in the file; the Git tab lists the services that run another image than the file's.
- A failed sync is notified (`git.sync.failed`) and retried; a sync that changed something can be notified too (`git.sync.succeeded`).
- Unlinking keeps the services as they are and makes them editable again.
- A sync downloads only the branch's last commit and unpacks it, so the manager briefly uses memory in proportion to that commit's files; it returns it right after. Keep large files (build outputs, binaries) out of the repository. Polling between commits only asks for the branch's commit, which costs next to nothing. The manager's memory may still read a few MB higher for a while after a sync: Linux keeps the files it just wrote in its cache and counts them, and drops them as soon as something else needs the memory.

### Git providers

Settings › **Git providers** gives the manager read access to private repositories, with no token to paste or rotate. A project then picks the provider, the repository and the branch from lists.

- **GitHub:** kipitiny creates a private GitHub App (on your account, or an organization's) that can only read repository contents, then you install it and choose its repositories. It reads them with tokens it mints for an hour at a time. When GitHub can reach the manager's address, the app also delivers push webhooks for every repository it reads. **Repositories** on the provider changes which ones it can read; GitHub Enterprise Server works too.
- **GitLab** (gitlab.com or self-hosted) and **Gitea** (or Forgejo): create an OAuth application on the forge with the redirect URI shown in kipitiny (GitLab: scopes `read_api` and `read_repository`), paste its ID and secret, then authorize it. kipitiny keeps the token refreshed. Push webhooks are set up per project, as above.
- A provider only reads repositories on its own server. It can't be removed while projects use it. Removing it in kipitiny doesn't delete the GitHub App or revoke the authorization on the forge: do that there.
- A revoked authorization or an uninstalled app fails the next sync (notified as `git.sync.failed`): **Reconnect** the provider.

## Moving a project to another kipitiny

1. On the old manager: project › **Compose** › **Export with secrets** (your password is asked again). Keep the zip safe: its `.env` holds every credential of the project.
2. On the new one: create the project, then **Compose** › **Import** with `compose.yaml` and `.env`.
3. Data moves with backups: back up the databases and volumes to a storage both managers reach, and restore them on the new services.
