---
title: Sleep mode
description: Stop rarely used projects when nobody uses them and start them on the next request
order: 17
---

A small server can host many projects that are rarely used: staging, demos,
previews, side projects. Sleep mode stops every service of a project after a
while without traffic, databases and workers too, and starts them again on
the next request, so an idle project costs no memory.

## Turning it on

In the project's *Settings*, set **Sleep mode** (in minutes, 5 minutes to 30
days). Empty never sleeps. It applies right away, without a deploy, and isn't
part of compose files, so a git-linked project keeps it across syncs. Agents
use the `set_sleep` MCP tool, the API `PUT /api/projects/{id}/sleep` with
`sleepAfterSeconds`.

A request through Traefik is what wakes a project, so it needs at least one
app with a domain and a container port, and no service whose traffic goes
around Traefik:

- apps with [published ports](/docs/services);
- apps on the host network.

Calls between services on the project network don't go through Traefik
either: they neither keep the project awake nor wake it. Since the whole
project sleeps and wakes together, that's only a concern for traffic from
outside the project.

## How it works

- Every minute the manager reads each server's Traefik request counters. A
  project whose apps' counters didn't move for its idle time is put to
  sleep: its apps stop, then its databases (containers are kept, so waking
  is a plain start). A stopped service stays as it is.
- The next visitor of any of its apps gets a **Starting…** page while the
  project starts: databases first, then the apps once their databases are
  ready, each after the services it `depends_on` in its
  [compose file](/docs/compose) (at most two minutes per service; jobs that
  already ran aren't run again). It reloads to the app as soon as a replica is ready, or says the
  app couldn't start after two minutes. API calls (requests that don't
  accept HTML) get `503 Service Unavailable` with `Retry-After` instead, so
  clients retry.
- Background workers and cron daemons inside the project don't run while
  it sleeps. Keep sleep for projects where that's fine, or use
  [scheduled jobs](/docs/jobs) instead.
- A deploy, a rollback, a start, a database backup or restore, a data query,
  a terminal and a [scheduled job](/docs/jobs) wake the project (a job keeps
  it awake while it runs). It falls asleep again after its idle
  time. A stop wins: a stopped service stays stopped, requests don't wake
  it.
- A sleeping service raises no "down" alert, and its [uptime
  check](/docs/monitoring#uptime-checks) pauses (a check would keep it awake).
- After a manager restart, every project counts as just used: none falls
  asleep before its full idle time.

The project's settings, its services and the canvas show it as **asleep**;
`get_app_status` reports `sleeping`.

The first request after a sleep waits for the project to start: a few
seconds for most, longer for heavy apps or databases. Keep sleep for projects
where that's fine.

## The starting page

Change it in the project's *Settings*, under **Starting page**, like
the [maintenance page](/docs/domains#maintenance-page): a title and a message,
or a whole HTML document of your own (up to 64 KiB). The manager adds a
`Refresh: 3` header, so a custom page reloads too without any script.
**Open the saved page** shows it without waking the project. Agents set it with
the `set_maintenance` MCP tool (`starting_title`, `starting_message`,
`starting_html`), the API with `starting` in
`PUT /api/projects/{id}/maintenance`. Like the maintenance page, it isn't part
of compose files.
