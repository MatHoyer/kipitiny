---
title: Monitoring
description: Service health, uptime checks from outside, notifications and the events they carry
order: 5
---

kipitiny watches services in two ways: from inside (are the containers running
and healthy?) and from outside (does the domain answer?). Both, and every
deploy, backup or update, can be sent to a notification channel.

## Service health

A deployed app is **healthy** when a replica of its current deployment runs
and passes its healthcheck (the image's, the compose `healthcheck`, or the
probe kipitiny injects; see [Deploys](/docs/deploys)). A service with no
healthy replica for **2 minutes** is reported (`service.unhealthy`), and again
when it recovers (`service.healthy`): a restart or a slow start pages no one.

The [reconciler](/docs/deploys) restarts crashed replicas on its own; each
restart is an event too (`service.restarted`).

## Logs

A service's **Logs** tab follows the output of its running replicas, merged in
time order (the last 200 lines first, then live; up to 2000 kept on the page).

- Lines are coloured by **level**, read from the common formats: JSON (`"level"`),
  logfmt (`level=`), `ERROR`/`WARN`/`INFO` words, nginx `[error]`, PostgreSQL
  `ERROR:`/`LOG:`, Redis and klog prefixes, and access-log statuses (5xx as
  errors, 4xx as warnings). The indented lines of a stack trace take the level
  of the line above. The app's own ANSI colours are kept.
- **Filter** by text, minimum level or replica; the error and warning counts
  filter to them in one click.
- **Pause** freezes the view while new lines keep arriving; **wrap**, a
  **download** of the lines shown, and **clear** complete the toolbar.

Deployment logs (on **Deployments**) are coloured the same way, with failures
and successes highlighted.

## Uptime checks

An uptime check requests an app's public URL, `https://<domain><path>`, on a
schedule, the way a visitor would: through DNS, TLS and Traefik. It catches
what container health can't, such as an expired certificate, a wrong DNS
record or a blocked port. Set it on the app's **Overview** (apps with a domain only),
or in the compose file as [`x-kipitiny.uptime`](/docs/compose#x-kipitiny-per-service);
a project synced from git can only set it there.

- **Path** (default `/`), **interval** 30 s to 1 h (default 60 s), **timeout**
  1 to 60 s (default 10 s, shorter than the interval).
- **Expected status**: a given HTTP status, or by default anything below 400.
- The service is **down** after 3 failed checks in a row, and **up** again after
  2 successful ones, so one slow answer doesn't flood the channels
  (`uptime.down`, `uptime.up`).
- The last 60 results are shown with their response time; hourly availability
  is kept for 30 days.
- A check pauses while the service is stopped or not deployed yet.

## Notifications

*Settings › Notifications* sends events to channels. A channel is a **Discord**
webhook (`https://discord.com/api/webhooks/…`). Each channel picks its events
(by default problems and recoveries, restores and new versions, but not
routine successes), and **Test** sends a sample message. A message links back
to the page in kipitiny. A problem that repeats, such as a crash-looping
service, is sent at most once per 15 minutes.

| Event | When |
|---|---|
| `deploy.succeeded`, `deploy.failed` | A deployment ends. |
| `service.unhealthy`, `service.healthy` | No healthy replica for 2 minutes, then recovered. |
| `service.restarted` | The reconciler restarted a service's container stopped outside kipitiny. |
| `uptime.down`, `uptime.up` | An uptime check failed 3 times in a row, then passed twice. |
| `backup.succeeded`, `backup.failed` | A backup ends (manual or scheduled). |
| `verify.succeeded`, `verify.failed` | A backup's restore test ends. |
| `restore.succeeded`, `restore.failed` | A restore ends. |
| `git.sync.succeeded`, `git.sync.failed` | A git sync changed something, or failed. |
| `cleanup.failed` | The scheduled [cleanup](/docs/cleanup) had errors. |
| `update.available` | A new kipitiny version is published (once per version). |

The AI agents connected through [MCP](/docs/api) see the same state with
`get_app_status` (containers, health, usage, uptime results) and
`get_deployment_log`.
