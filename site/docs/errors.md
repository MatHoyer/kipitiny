---
title: Error tracking
description: Errors of your apps, sent by any Sentry SDK, grouped into issues and tied to the deploy that brought them
order: 22
---

Error tracking catches the errors your apps throw, groups the identical ones
into **issues**, alerts you about new ones, and tells which deploy brought each
in. Apps send them with an official [Sentry SDK](https://docs.sentry.io/platforms/)
(JavaScript, Python, Go, PHP, Ruby, Java, .NET…): kipitiny speaks the same
protocol, so there is nothing of kipitiny's to install. The errors are kept
by the [analytics](/docs/analytics) server, in its own SQLite file: no extra
container.

## Turning it on

1. Turn [analytics](/docs/analytics#turning-it-on) on in **Settings ›
   Analytics**: its domain also receives the errors.
2. In an app's **Settings › Networking**, turn on **Error tracking** (or set
   `errors: true` in its [`x-kipitiny`](/docs/compose#x-kipitiny-per-service) block).
   The app gets its own **DSN**, the address its SDK reports to:
   `https://<key>@stats.example.com/<number>`.
3. Deploy. Each deploy sets these variables, unless the app sets them itself:

| Variable | Value |
|---|---|
| `SENTRY_DSN` | the app's DSN |
| `SENTRY_RELEASE` | the deployed image's tag (`sha-a1b2c3`), or the start of its digest |
| `SENTRY_ENVIRONMENT` | `production`, or `preview-<name>` in a [preview](/docs/previews) |

4. Initialize the SDK. Server SDKs read the variables by themselves:

```python
import sentry_sdk
sentry_sdk.init()  # SENTRY_DSN, SENTRY_RELEASE, SENTRY_ENVIRONMENT
```

```js
// Node
import * as Sentry from "@sentry/node";
Sentry.init({ dsn: process.env.SENTRY_DSN });
```

A browser bundle can't read the server's environment: put the DSN (shown on
the app's **Errors** tab) in its `Sentry.init`, or render it into the page.
The DSN's key is public by design, like Sentry's: it only lets someone send
errors to that app.

Previews keep the setting: each preview's apps report to their own DSN,
under `preview-<name>`, and their errors never trigger alerts.

## Issues

The app's **Errors** tab lists its issues with how many events and users
each had, when they were first and last seen, their last 24 hours, and the
release that brought them in. **Open**, **Resolved** and **Ignored** filter
them; search looks in titles and the place they were thrown.

An issue's page shows its latest events one at a time: the exceptions with
their stack (the app's own frames first, the source around the line when the
SDK sends it), the breadcrumbs (what happened just before: requests, clicks,
logs), the tags, the user, the browser or runtime. When the app gives Sentry
and `kipitiny.identify` the same user id, the user's
[visits](/docs/analytics#visits) can be found by it.

- **Resolve** when the fix is deployed. If the error happens again, the issue
  reopens as a **regression**.
- **Ignore** keeps counting it, without showing it among the open ones or
  alerting.

### Grouping

Events are grouped by the raised exception's type and the three innermost
frames of the app's own code (functions and files, not line numbers, so an
issue survives unrelated edits). Without a stack, by the message with its
numbers and ids left out. A bundle's content hash in a file name doesn't
count either. An SDK's `fingerprint` replaces this, `{{ default }}` standing
for it.

Minified browser code is shown as sent: source maps aren't supported, so
mangled function names may give a new issue per build.

## What is kept

- The latest **100 events** of each issue, for **30 days**. Past 10 events of
  one issue in a minute (a crash loop), the others are counted, not kept.
- Issues, for as long as analytics keep their data (365 days by default),
  counted from their last event.
- Of each event: exceptions, stack frames with their source lines,
  breadcrumbs (URLs without their query values), tags, release, environment,
  the user's id or username, the request's URL and method, the country (behind
  Cloudflare). Request bodies, headers, cookies, local variables and extra
  data are dropped; emails and IP addresses only count distinct users,
  hashed.

## Limits

- An app's SDK may send 20 requests a second, in bursts of 200; above that
  it is told to wait 10 seconds. A request is at most 1 MB (4 MB
  decompressed).
- Bodies are taken uncompressed or as gzip, deflate or zstd. sentry-python
  compresses with brotli when the `brotli` package is installed: set
  `transport_compression_algo` to `gzip` in its `_experiments`.
- Only errors and messages are kept: performance traces, sessions, replays,
  logs and attachments are accepted and dropped.

## Who sees what

Anyone who can view a project sees its apps' issues. Resolving, ignoring or
reopening one needs the *deploy* permission; turning error tracking on or off,
*settings*. The [MCP](/docs/api) tools `list_issues`, `get_issue` and
`set_issue_status` give agents the same, stack traces included, so an agent
can read an error and fix it.
