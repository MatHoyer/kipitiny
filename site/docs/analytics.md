---
title: Analytics
description: Cookieless web analytics of your apps, kept on your own server
order: 20
---

kipitiny can count visits of your apps' domains: pageviews, visitors, where
they come from and what they use, without cookies or a consent banner. A small
analytics server runs next to Traefik on the manager's server and keeps the
data in its own volume; nothing goes to a third party, and the manager itself
stores none of it.

## Turning it on

1. In **Settings › Analytics**, click **Enable analytics** and give it a
   **domain**, e.g. `stats.example.com`. It serves the tracker script and
   receives the visits, over HTTPS like an app. Point its DNS at the server (with
   [Cloudflare connected](/docs/domains) the record is created for you). The
   dialog lists what happens; **Enable** runs it step by step.
2. In an app's **Settings › Networking**, turn on **Analytics** (or set
   `analytics: true` in its [`x-kipitiny`](/docs/compose#x-kipitiny-per-service) block). The app needs a
   domain and a container port. Applies right away, no deploy.
3. Add the script to the app's pages, e.g. in the `<head>`:

```html
<script defer src="https://stats.example.com/a.js"></script>
```

The app's **Analytics** tab shows the numbers, and the exact tag to copy.

The first time, the manager pulls `ghcr.io/mathoyer/kipitiny-analytics` (the
same version as the manager), a container of a few MB of memory.

## What is counted

- **Visitors**, **visits** (a visit ends after 30 minutes without activity),
  **pageviews**, **bounce rate** (visits of one page), **visit duration**, and
  the visitors **active** in the last 30 minutes.
- Top **pages**, **entry** and **exit pages**, **referrers** (other sites only),
  **UTM** source, medium and campaign, and the **keys** of query parameters
  (their values may be tokens, so they are not kept).
- **Browsers**, **operating systems**, **devices** (from the user agent and
  screen size), **screen sizes** and **languages**.
- **Countries**, when the domain is behind Cloudflare's proxy (from its
  `CF-IPCountry` header; there is no GeoIP database).
- Single-page apps: each `history.pushState` navigation is a pageview.

Bots, crawlers, link previews and scripts are left out by their user agent.

### Visits

Under the lists, **Visits** shows the range's visits, newest first: when,
who (country, browser, OS and device, or the identity below), where they came
from, pages, events and duration. Click one to see its journey: the pages in
order with their times and titles, and the custom events with their
properties.

A visitor is only recognized **within a day** (see [Privacy](#privacy)): the
same person tomorrow is a new visitor, and their visits aren't linked.

### Identifying users

An app that already knows who is signed in can name the visitor:

```js
kipitiny.identify("user-42", { plan: "pro" });
```

The id (up to 128 characters) is kept on the visit, with its properties, and
sent with every hit of the page after it. It shows in **Visits** and in the
**Identified users** list; clicking one filters the tab to that user's
visits, across days.

This links one person's visits over time, which the cookieless counting
otherwise never does: whether your users agreed to it is your app's question,
as for any data you keep about them. Prefer an opaque id over an email. It's
stored in the analytics volume only, and deleted with the visit after the
retention.

### Filters

Click a value in any list (a page, a referrer, a country…) to narrow the whole
tab to it; filters add up, and each chip removes its own. A filter on a page,
referrer, UTM tag, query key, browser, OS, device, screen, language or country
keeps the matching pageviews and events; one on an entry or exit page, a
custom event or an identified user keeps the visits that have it. Filters
apply to **Visits** too. `get_analytics` and `list_visits` take the same
filters, as `filters: ["countries:FR", "pages:/pricing"]`.

### Custom events

```js
kipitiny.track("signup", { plan: "pro" });
```

or, without code, on what gets clicked:

```html
<button data-kipitiny-event="signup" data-kipitiny-event-plan="pro">Sign up</button>
```

Up to 20 properties per event, strings, numbers or booleans. Events and their
properties have their own lists in the Analytics tab.

## Privacy

No cookie, no `localStorage`, no fingerprinting script. A visitor is a hash of
the site, the IP address and the user agent with a **salt that changes every
day**: the same browser counts once per day, and can't be followed from one day
to the next (unless the app [identifies](#identifying-users) them). IP
addresses are never stored. Visits older than the retention
(**365 days** by default, Settings › Analytics) are deleted every hour.

## Who sees what

Anyone who can view a project sees its apps' analytics. Turning an app's
analytics on or off needs the *settings* permission; the server's settings are
admin-only. The [MCP](/docs/api) tools `get_analytics`, `list_visits` and
`get_visit` return the same numbers and journeys to agents.

## Limits

- Apps on the manager's server only, for now.
- [Previews](/docs/previews) are never counted.
- **Disable** stops the server and keeps its data; **Delete analytics data**
  (shown while it's off) removes it for good.
