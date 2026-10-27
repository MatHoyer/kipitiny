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
      uptime: {path: /healthz}
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

`db` and `cache` become a managed PostgreSQL and Redis (official images are recognised), `web` connects to them through references, and `API_KEY` comes from the project variable `API_KEY` (or a `.env`, which makes it a secret). Plain compose keys say what compose already has words for (the port, the healthcheck); `x-kipitiny` holds only what compose has none for: the domain, the pre-deploy command, the uptime check, Traefik's middlewares.

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
| `ports` | Host ports published straight to the container, for traffic that isn't HTTP: `"25565:25565"`, `"9000:9000/udp"`, a range `"8000-8010:8000-8010"`, or the long form `{target: 25565, published: 25565, protocol: tcp, host_ip: 127.0.0.1}`. A host IP (`"127.0.0.1:5432:5432"`, `"[::1]:5432:5432"`) binds that address only, e.g. the server itself; without one, every address. At most 20 ports. An app with published ports runs one replica. HTTP goes through `expose` (or `x-kipitiny.port`) instead. |
| `volumes` | Named volumes: `name:/path` (`:ro` for read-only) or `{type: volume, source: name, target: /path, read_only: true}`. Shared by the service's replicas, kept across deploys, backed up, managed from the [Files](/docs/files) tab. At most 10. Names: lowercase letters, digits and dashes. An anonymous volume (`/app/node_modules`) is kept as a named one after its path (`app-node-modules`), with a warning. Bind mounts: see [Files](#files). |
| `configs`, `secrets` | Files written into the container: see [Files](#files). |
| `env_file` | `.env`-style files of the git repository, merged into `environment` (which wins): a path, a list, or `{path: …, required: false}`. Git projects only; elsewhere, use `environment`, a `.env` or the project variables. |
| `network_mode` | Only `host`: the app runs in the server's network. See [Host access](#host-access). |
| `deploy` | `replicas` (1–10; databases always 1), `resources.limits.memory` / `resources.limits.cpus`, `resources.reservations.memory` (memory Docker tries to leave the app when the server runs short, at most the limit), and `restart_policy` (`condition: none` is `restart: "no"`, `on-failure` with `max_attempts` is `restart: on-failure:N`, `any` the default). `update_config` and `rollback_config` are ignored: kipitiny starts the new replicas, moves traffic once they're ready, then stops the old ones. |
| `mem_limit` | Same as `deploy.resources.limits.memory` (`512m`, `1g`, bytes). |
| `cpus` | Same as `deploy.resources.limits.cpus` (cores, `0.5` = half a core; 0.01–256). |
| `stop_grace_period` | Time an app gets to exit after SIGTERM before it is killed (`30s`, `1m30s`; at most 10 minutes; default 10 s). Not for databases. |
| `command` | Replaces the image's `CMD`: a list (`["worker", "--queue", "mail"]`) or a string split like a shell does, without expanding variables (use `sh -c '…'` for that). Lets several services run the same image differently. |
| `entrypoint` | Replaces the image's `ENTRYPOINT`, same forms as `command`; `""` clears it. The pre-deploy command ignores both. |
| `user` | The user the container runs as: a name or UID, optionally `:group` (`"1000:1000"`). |
| `working_dir` | The directory the command starts in (an absolute path). |
| `labels` | Docker labels added to the container, as a mapping or a `key=value` list. `traefik.*` and `kipitiny.*` labels are the manager's: dropped with a warning (route with `x-kipitiny.domain`). |
| `tmpfs` | In-memory mounts, emptied on every start: `/run` or `/tmp:size=64m,mode=1777`, one or a list. A volume of `type: tmpfs` (with `tmpfs.size` / `tmpfs.mode`) does the same. |
| `shm_size` | Size of `/dev/shm` (`256m`, `1g`); Docker's default is 64 MB. Browsers and some databases need more. |
| `ulimits` | Process limits: `nofile: 65535`, or `nofile: {soft: 1024, hard: 4096}`. |
| `sysctls` | Namespaced kernel parameters, as a mapping or a `key=value` list: `net.core.somaxconn: 1024`. An app in the host network can't set `net.*` ones. |
| `read_only` | `true` mounts the root filesystem read-only; volumes and `tmpfs` stay writable. |
| `init` | `true` runs a small init as PID 1 that forwards signals and reaps zombie processes. |
| `stop_signal` | The signal a stop sends first (`SIGINT`, `SIGQUIT`…), then `SIGKILL` after `stop_grace_period`. |
| `tty`, `stdin_open` | Give the container a terminal and keep its stdin open, for images that expect one. |
| `extra_hosts` | Entries added to `/etc/hosts`: `["db.local:10.0.0.2"]` or a mapping. `host-gateway` as the IP is the server itself. |
| `cap_add`, `cap_drop` | Linux capabilities to add or remove (`NET_ADMIN`, `SYS_TIME`…, with or without `CAP_`). kipitiny drops `NET_RAW` from every app unless it's added back. See [Host access](#host-access). |
| `devices` | Host devices mapped into the container: `/dev/net/tun`, `/dev/ttyUSB0:/dev/ttyACM0:rwm`. See [Host access](#host-access). |
| `privileged` | `true` gives the container every capability and device. See [Host access](#host-access). |
| `security_opt` | Security options: `no-new-privileges:true`, `apparmor=unconfined`, `seccomp=unconfined`… See [Host access](#host-access). |
| `networks` | The networks the service is on, a list or a mapping (aliases, addresses and other options are ignored). Without the key, it's on the project's network with every other service. Networks declared at the top level are the project's own: services share only the networks they're both on, so a `back` network keeps the database away from the public front. `default` is the project network. A network declared `external: true` (with `name:` when it's named otherwise) is a [network created by hand](/docs/services#networks) with that name on the project's server, shared with other projects; it must exist. A service listing networks gets exactly the networks created by hand it lists; one without the key keeps those set in the UI. A service listing only external networks stays on the project network too. Top-level options other than `external` and `name` are ignored. |
| `profiles` | `[preview]` makes a service of [previews](/docs/previews) only (`x-kipitiny.preview: only`). A service with other profiles only is skipped, with a warning: kipitiny runs no other profile. |
| `depends_on` | Services of the file started first, on deploy and when the project [wakes up](/docs/sleep): a list of names, or `{db: {condition: service_healthy}}`. `service_started` (the default) only orders; `service_healthy` waits until it's ready (its healthcheck, or running for 5 s); `service_completed_successfully` waits until a job exits 0. When the deploy of such a service fails, its dependents aren't deployed: their deployment shows as *skipped*, and their previous version keeps running. Databases always start first. Unknown names and cycles are errors; `required` and `restart` are ignored. Renaming a service updates the references. |
| `restart` | `always` and `unless-stopped` are kipitiny's own behaviour: a stopped container is started again. `"no"` makes the service a **job** (a migration, a seed): a deploy waits until it exits 0 (at most 3 minutes), it isn't restarted afterwards nor when the project wakes up, and a job that exited 0 counts as healthy. `on-failure` (or `on-failure:3`, the retries) lets Docker restart it while it fails; it counts as ready once it runs like an app, or exits 0. |
| `extends` | Starts from another service: `extends: base`, or `{service: base, file: common.yml}` from another file of the repository (git projects only). Mappings such as `environment` merge, `ports`, `volumes` and other lists add up (a volume, config or secret at the same target replaces the base's), other keys replace the base's. Relative paths in the other file stay relative to it. |
| `build` | Ignored when `image` is set (with a warning); an error without one. |
| `x-kipitiny` | kipitiny's settings, below. |

Top level: `name` (the project name, informational), `services`, `volumes`, `configs`, `secrets`, `include` and `x-kipitiny` (project variables). `include: [db/compose.yml]` (or `{path: …}`) adds the services of other files of the repository, each read with paths relative to itself; git projects only. A service defined in two files is an error; their project variables are added unless already set. `project_directory` and `env_file` are ignored. A top-level volume may be `external: true` (with `name:` when the Docker volume is named otherwise): an existing volume, mounted as is and never removed by kipitiny. Or it may set the local driver's `driver_opts` (`type`, `o`, `device`), e.g. an NFS share, used when the volume is created. Both are admin-only, see [Host access](#host-access); other drivers are refused, other options ignored.

### Ignored with a warning

kipitiny networks services itself, so these are dropped and reported: `container_name`, `hostname`, `links`, `logging`, `pull_policy`, and the top-level `version`. A database's `command`, `user`, `depends_on` and the like are dropped too: the manager runs it, and databases always start first. Other `x-*` keys are ignored silently.

### Errors

Any other key is refused, because ignoring it would run something different from what the file says: for example `platform`, `cgroup`, `pid`, `ipc`. So is any `network_mode` but `host`.

### Files

Files a service needs, such as a config, a certificate or a script, are written into its containers before they start, so they work on remote servers too. They come from:

- **Top-level `configs`**: `content:` (written as is, `$$` for a literal `$`) works in any project; `file: ./path` reads the repository and `environment: NAME` a compose variable of the `.env` (unlike secrets, not a project variable). A service lists them as `configs: [name]` (at `/name`) or `{source: name, target: /etc/app.conf, mode: 0440}`.
- **Top-level `secrets`**: the same, at `/run/secrets/name` by default (a relative `target` goes under `/run/secrets`). `environment: NAME` takes the value from the `.env`, else becomes `{{ project.NAME }}`, resolved at deploy like the env's [references](#values-references-and-secrets) (password manager ones too). An export writes them as variables, valued in the `.env`.
- **Bind mounts of the repository**: `./nginx.conf:/etc/nginx/nginx.conf` or a directory, `./conf:/etc/app`, relative to the compose file. Git projects only, read at each sync: a change in the repository redeploys the service. They're copies: `:ro` is ignored, and nothing the container writes goes back.

At most 100 files and 4 MB per service, not in a volume or `tmpfs` (use a volume for data the app writes), nor with `read_only`. `uid` and `gid` are ignored: files belong to root. Their contents don't show in the API or the UI, only their paths and sizes.

Absolute bind mounts (`/srv/media:/media`) mount a path of the server: admin-only, see [Host access](#host-access). The Docker socket, `/var/run/docker.sock:/var/run/docker.sock` (`:ro` for read-only), has its own setting. Paths starting with `~` are refused.

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
- **Capabilities, devices, privileged mode and security options** (`cap_add`, `devices`, `privileged`, `security_opt`) are for VPNs, network tools and hardware (a Zigbee stick, a GPU, `/dev/net/tun`). `privileged` and most capabilities let the app escape its container.
- **Absolute bind mounts** (`/srv/media:/media`) mount a path of the server, `:ro` for read-only.
- **External volumes and driver options** reach data beyond the app's own: another volume on the server, a network share, or (with `o: bind`) any path of the host. When an admin mounts one `:ro`, only an admin makes it writable or restores a backup into it.
- Any of these gives the app control of the server: only use images you trust. Databases can't have them, previews leave such services out, and only admins can turn them on (a member may still change the app's other settings, or remove them).
- A service runs on its project's server: to monitor several servers, make one project per server. The Beszel hub itself is a normal app (`henrygd/beszel`, port 8090, a volume at `/beszel_data`). Both are also [one-click templates](/docs/templates).

### `x-kipitiny` (per service)

| Field | Type | Applies to | Meaning |
|---|---|---|---|
| `kind` | `app` \| `postgres` \| `redis` \| `mysql` \| `mariadb` \| `mongodb` | all | What the service is. Default: `postgres` or `redis` when the image is the official one (`postgres:17-alpine`, `redis:8`), else `app`. Set `kind: app` to run those images as plain containers. MySQL, MariaDB, MongoDB and PostGIS (`postgis/postgis`) images stay apps unless `kind` says otherwise (`postgres` for PostGIS), so existing files that run them with their own `environment` and `volumes` keep working. |
| `domain` | hostname | apps | Public hostname, served over HTTPS by Traefik. Needs `port` (unless the app only publishes ports and the domain is only for DNS). |
| `port` | 1–65535 | apps | Container port Traefik routes the domain to. Usually written as `expose` instead. |
| `health_path` | path | apps | HTTP path (e.g. `/healthz`) that must answer 2xx/3xx before a new replica takes traffic, checked by a probe kipitiny injects, so the image needs no `curl`. Needs the port. Without it or a `healthcheck`: the image's healthcheck, else a stability wait. Not with `healthcheck`. |
| `pre_deploy` | shell command | apps | Runs once (`sh -c`) in a one-off container before a rollout, e.g. migrations. A failure aborts the deploy. |
| `pre_backup` | shell command | apps | Runs in a running replica before each volume backup, e.g. to flush to disk. |
| `icon` | name | all | Logo the UI shows (e.g. `ghost`, `n8n`). Default: from the kind or image. |
| `analytics` | boolean | public apps | Count the domain's visits with [analytics](/docs/analytics), once they're on in Settings. Applies without a deploy; left out of previews. |
| `preview` | `only` \| `skip` | all | `only`: the service runs in [previews](/docs/previews) alone, e.g. a mail catcher, never in the project itself (same as `profiles: [preview]`). `skip`: left out of previews, e.g. a scheduler emailing real customers. Without git, kept on the service (its **Previews** setting): an `only` service is created but never deployed in the project. |
| `tags` | list of names | all | [Tags](/docs/services#tags) grouping the service in the UI (up to 10, lowercased). Without it, the service has none: applying the file removes the ones set in the UI. |
| `secrets` | list of env keys | apps | Which `environment` entries are secrets (write-only in the UI and API, masked everywhere, exported only with secrets). Without it, the values given as `${NAME}` are (see [Values](#values-references-and-secrets)). |
| `password` | string, usually `${NAME}` | databases | The database's password at creation, to keep the credentials of a moved database. Empty generates one. It can't change afterwards. |
| `middlewares` | mapping | public apps | Applied by Traefik to every request, below. |
| `uptime` | mapping | public apps | An [uptime check](/docs/monitoring#uptime-checks) of the domain, below. `uptime: {}` checks `/` with the defaults. Without it, the app has no check: applying the file removes one set in the UI. |
| `jobs` | mapping of names | apps | [Scheduled jobs](/docs/jobs), below. Without it, the app has none: applying the file removes the ones set in the UI. Left out of previews. |
| `anonymize` | mapping of tables | PostgreSQL, MySQL, MariaDB, MongoDB | How [previews seeded with anonymized data](/docs/previews#anonymized-seeds) get this database: rules by table and column, below. `anonymize: {}` says it holds nothing personal. Without it, the database has no rules: applying the file removes the ones set in the UI. Applies without a deploy. |

`middlewares`:

| Field | Meaning |
|---|---|
| `basic_auth` | Up to 20 users, each with a `name` and either a `hash` (bcrypt, e.g. from `htpasswd -nbB user pass`) or a `ref` (a password manager reference `{{ pass://Vault/Item/field }}`, hashed at deploy). Plain passwords are refused: they'd sit in the file. |
| `ip_allowlist` | Up to 50 IPs or CIDR ranges allowed to connect. |
| `rate_limit` | `average` requests per second per client IP, with bursts up to `burst` (defaults to `average`). |
| `headers` | Response headers to set (up to 20); an empty value removes the header. |

`uptime`:

| Field | Meaning |
|---|---|
| `path` | Path requested on the domain. Default `/`. |
| `interval` | Time between checks, 30 s to 1 h (`30s`, `5m`, or seconds). Default 60 s. |
| `timeout` | 1 to 60 s, shorter than the interval. Default 10 s. |
| `expected_status` | The HTTP status that means up. Default: any below 400. |
| `paused` | `true` keeps the check and its history without running it. |

`jobs`, each under its name (lowercase letters, digits, dashes):

```yaml
x-kipitiny:
  jobs:
    purge-sessions: {command: node scripts/purge.js, schedule: "*/15 * * * *"}
    nightly-report:
      command: node scripts/report.js --to $REPORT_TO   # x-kipitiny isn't interpolated: the shell reads it
      schedule: CRON_TZ=Europe/Paris 0 7 * * 1-5
      timeout: 10m
```

| Field | Meaning |
|---|---|
| `command` | Run with `sh -c` in a one-off container of the app's deployed image. Required. |
| `schedule` | Cron, UTC unless prefixed with `CRON_TZ=<zone>`, or `@hourly`, `@daily`... Required. |
| `timeout` | 1 s to 24 h (`90s`, `10m`, or seconds). Default 1 h. |
| `paused` | `true` keeps the job and its runs without scheduling it. |

`anonymize`, by table (`schema.table` outside PostgreSQL's `public`; a collection of the service's own database for MongoDB):

```yaml
x-kipitiny:
  anonymize:
    users:
      email: fake:email        # u1f3a…@example.invalid, unique per address
      name: fake:name
      phone: fake:phone        # same shape, first two digits kept
      password_hash: fixed:$2b$10$7EqJtq98hPqEX7fNZaFWoO   # everyone logs in with the same password
      notes: null
    sessions: truncate
    audit_log: exclude
```

| Rule | Meaning |
|---|---|
| `truncate` (table) | Kept, empty. Tables referencing it must be truncated too. |
| `exclude` (table) | Dropped, with the views and foreign keys on it. |
| `keep` | Unchanged; the same as no rule. |
| `null` | Emptied. |
| `hash` | A salted MD5 (32 hex characters; a valid UUID on a `uuid` column). |
| `fixed:<value>` | Every row gets the value. |
| `fake:email` \| `fake:name` \| `fake:phone` \| `fake:text` | A made-up value of that kind: an `@example.invalid` address, a name, digits in place of digits, lorem ipsum of the same length. |

`hash` and `fake:` values derive from the original through the project's secret salt: the same value always gives the same result, in every table, so unique keys and joins keep working, and nobody without the salt can match one against a list of known values. Empty values stay empty.

### Databases

`postgres`, `redis`, `mysql`, `mariadb` and `mongodb` services are managed: one replica, a data volume of their own, a generated password, private to the project, backed up and restore-tested.

- Only `image`, the memory and CPU limits, `icon`, `tags`, `password`, the user and database name and init scripts apply. `volumes`, `ports` and the rest of `environment` are ignored with a warning; `stop_grace_period`, `domain` and `middlewares` are errors.
- From `environment`, a new database takes its user and database name (`POSTGRES_USER` / `POSTGRES_DB`, `MYSQL_USER` / `MYSQL_DATABASE` or the `MARIADB_` ones, `MONGO_INITDB_ROOT_USERNAME` / `MONGO_INITDB_DATABASE`: letters, digits and underscores) and its password (`POSTGRES_PASSWORD` and the like, as `x-kipitiny.password`). Like the password, they're fixed at creation: a later change is reported and kept as is. An export writes them, so a moved database keeps them.
- Files in `/docker-entrypoint-initdb.d` (a [config or a bind mount](#files) of the repository: `./init.sql:/docker-entrypoint-initdb.d/init.sql`) run on the database's first start, as the official images do; changing them later redeploys nothing (they only run on an empty database). Not for Redis.
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
| `{{ app.SERVICE.URL }}`, `{{ app.SERVICE.DOMAIN }}` | At every deploy | Where an app of the project is public: `https://shop.example.com`, or just the host. Write `APP_URL`, OAuth callbacks or CORS origins with it rather than the domain itself, so a copy of the project (a [preview](/docs/previews)) points at its own apps. A deploy fails if the app has no domain. |
| `{{ pass://Vault/Item/field }}` | At every deploy | A secret read from a connected password manager; never stored by kipitiny. `pass+work://` reads it through the connection named `work`. |

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
4. Created services, services whose change needs it (anything but `replicas`, `icon` and `tags`), and services that never deployed successfully (a first deploy that failed) are deployed: databases first, then apps once the databases are up, each after the deploys of the services it [`depends_on`](#compose-keys-kipitiny-reads). Scaling and `depends_on` apply at once.

A service can't change kind (an app into a database): delete it first.

## Git

Project › **Git**: the repository, a branch (default `main`) and the file's path (default `compose.yaml`). A public repository is just its HTTPS URL; a private one is picked from a [git provider](#git-providers). Linking shows what the first sync will change.

- The branch is checked every 5 minutes by default (60 s to 24 h, or never with auto sync off). A push webhook syncs at once. Through a GitHub provider whose app has webhooks, there's nothing to set up. Otherwise the URL and secret are on the Git tab: GitHub and Gitea sign with the secret, GitLab sends it as a token, anything else as `Authorization: Bearer <secret>`. **Sync now** applies the file by hand, **Preview** shows what it would change.
- The file owns the services, their uptime checks and their jobs. Creating, editing or deleting them any other way (UI, API, MCP) is refused; edit the file. A database the file dropped can still be deleted by hand. Project variables, backups and schedules stay editable: they are not in the file.
- CI can still deploy another tag of an app's image (`kipitiny deploy --tag`). The service keeps it until a commit changes that service's block in the file; the Git tab lists the services that run another image than the file's.
- A failed sync is notified (`git.sync.failed`) and retried; a sync that changed something can be notified too (`git.sync.succeeded`).
- A sync acts with the permissions of the user who linked the repository (within the token's restriction if a restricted token linked it). When an admin linked it, **anyone who can push to the branch can do what that admin can**, including giving an app [host access](#host-access), which is control of the server. Link as an admin only repositories whose push access you'd trust with the server, or protect the branch.
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
3. Data moves with backups: back up the databases and volumes to a storage both managers reach, add that storage on the new manager (with the old one's key if it is encrypted) and [find its backups](/docs/backups#moving-to-another-manager), then restore them on the new services.
