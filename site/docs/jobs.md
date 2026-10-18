---
title: Scheduled jobs
description: Run a command of an app on a cron schedule, in a one-off container of its image
order: 21
---

A scheduled job runs a command on a cron schedule: a nightly report, a cache
purge, a queue sweep. It runs in a **one-off container** of the app, so the
app needs no cron daemon and no extra service.

## Adding a job

On an app's **Jobs** tab, **New job** asks for:

- a **name**, unique within the app (lowercase letters, digits and dashes);
- the **command**, run with `sh -c` (the image needs a shell; its entrypoint
  is skipped, like the [pre-deploy command](/docs/deploys));
- the **schedule**, a cron expression in UTC (`0 3 * * *`), a descriptor
  (`@hourly`, `@daily`), or `CRON_TZ=Europe/Paris 0 3 * * *` for another time
  zone;
- a **timeout**, 1 second to 24 hours (one hour by default): a run still going
  then is stopped and fails.

A paused job keeps its history but isn't scheduled; **Run now** still runs it.
Only apps have jobs, and only once deployed.

Jobs can also be written in the compose file, as
[`x-kipitiny.jobs`](/docs/compose#x-kipitiny-per-service). A project synced
from git can only set them there (**Run now** still works); jobs of a git
project don't run in its [previews](/docs/previews).

## What a run gets

Each run starts a new container from the app's **current deployment**: the
same image (pinned to its digest), environment, secrets, networks, volumes,
memory and CPU limits. It joins no public route and publishes no port. It
reaches the project's databases and other services by name, like the app does.

The container is removed once the run ends; the run keeps its status, exit
code, duration, who started it (empty for the schedule) and the last 2000
lines of its output. Each job keeps its last 50 runs.

- A job runs **once at a time**: a run that comes due while the previous one
  still runs is skipped, and **Run now** is refused.
- A job of a **stopped** app doesn't run. One of a project in
  [sleep mode](/docs/sleep) wakes it first, and the project stays awake while
  the job runs.
- A manager restart or update stops the runs in progress (they fail as
  *interrupted*); containers left by a crash are removed by the reconciler.
- Deleting a job, or its app, stops its run and removes its history.

## Notifications

A run that exits non-zero or times out sends `job.failed` to the
[notification channels](/docs/monitoring#notifications) that picked it (a job
failing every minute pages at most once per 15 minutes); `job.succeeded` is
there for channels that want every run. New channels get `job.failed` by
default; existing ones can add both.

## Permissions

Viewing jobs, runs and logs needs **view**; adding, changing and deleting jobs
needs **settings**; **Run now** needs **deploy**. See [Users](/docs/users).

## MCP and API

Agents use `list_jobs`, `set_job`, `delete_job`, `run_job` and
`list_job_runs` (runs with the latest log). The API has
`GET|POST /api/services/{id}/jobs`, `PUT|DELETE /api/jobs/{id}`,
`POST /api/jobs/{id}/run`, `GET /api/jobs/{id}/runs` and
`GET /api/jobs/{id}/runs/{run}/log`.
