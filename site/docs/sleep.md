---
title: Sleep mode
description: Stop rarely used apps when nobody uses them and start them on the next request
order: 17
---

A small server can host many apps that are rarely used: staging, demos,
previews, side projects. Sleep mode stops an app's containers after a while
without traffic and starts them again on the next request, so idle apps cost
no memory.

## Turning it on

In the app's *Settings › Image & runtime*, set **Sleep after idle** (in
minutes, 5 minutes to 30 days). Empty never sleeps. It applies right away,
without a deploy. In a compose file it is `x-kipitiny.sleep_after` (`30m`,
`2h`, see [Compose files](/docs/compose)); agents use the `set_sleep` MCP
tool, and the API takes `sleepAfterSeconds` on `PATCH /api/services/{id}`.

Only apps with a domain and a container port can sleep: a request through
Traefik is what wakes them. Not:

- databases;
- apps with [published ports](/docs/services) (their traffic doesn't go
  through Traefik);
- apps that other services call on the project network only: those calls
  don't go through Traefik either, so they neither keep the app awake nor
  wake it.

## How it works

- Every minute the manager reads each server's Traefik request counters. An
  app whose counters didn't move for its idle time is put to sleep: its
  containers are stopped (kept, so waking is a plain start).
- The next visitor gets a **Starting…** page while the containers start. It
  reloads to the app as soon as a replica is ready, or says the app couldn't
  start after two minutes. API calls (requests that don't accept HTML) get
  `503 Service Unavailable` with `Retry-After` instead, so clients retry.
- A deploy, a rollback or a start wakes the app. A stop wins: a stopped app
  stays stopped, requests don't wake it.
- A sleeping app raises no "down" alert, and its [uptime
  check](/docs/monitoring#uptime-checks) pauses (a check would keep it awake).
- After a manager restart, every app counts as just used: none falls asleep
  before its full idle time.

The app's page and the project canvas show it as **asleep**;
`get_app_status` reports `sleeping`.

The first request after a sleep waits for the app to start: a few seconds
for most apps, longer for heavy ones. Keep sleep for apps where that's fine.

## The starting page

Change it in the app's *Settings › Maintenance*, under **Starting page**, like
the [maintenance page](/docs/domains#maintenance-page): a title and a message,
or a whole HTML document of your own (up to 64 KiB). The manager adds a
`Refresh: 3` header, so a custom page reloads too without any script.
**Open the saved page** shows it without waking the app. Agents set it with
the `set_maintenance` MCP tool (`starting_title`, `starting_message`,
`starting_html`), the API with `starting` in
`PUT /api/services/{id}/maintenance`. Like the maintenance page, it isn't part
of compose files.
