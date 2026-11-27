import type { AnalyticsSettings, Tracker, Cleanup, Notifications, Connection } from "@/api";
import { demoDeployErrors, demoErrorSeries, demoIssue, demoIssueEvent, demoIssues, setDemoIssueStatus } from "./errors";

import { EVENTS, progressed, type Route } from "./ops";
import { HttpError, MASK, db, dbDemo, emailConfig, emailOptions, invalid, newId, notFound, now, save, serviceOf, users } from "./state";
import { demoAnalytics, demoVisit, demoVisits, trackerURL } from "./views";

export let thresholds: Notifications["thresholds"] = { memoryPercent: 90, memoryMinutes: 5, cpuPercent: 90, cpuMinutes: 10, diskFreePercent: 10 };

export let trackers: Tracker[] = [
  { id: "trk-1", kind: "link", slug: "launch", name: "Launch post", target: "https://acme.example/blog/launch?utm_source=twitter", createdAt: 1790000000, hits: 312, url: "" },
  { id: "trk-2", kind: "pixel", slug: "k3xa9qpz", name: "October newsletter", createdAt: 1790500000, hits: 1240, url: "" },
];

export let analyticsSettings: AnalyticsSettings = { enabled: true, domain: "stats.acme.example", retentionDays: 365 };

/** The demo's cleanup run by hand: when it started, while it walks through its steps. */
export let cleanupStart: number | null = null;

export let cleanupLast: Cleanup["lastRun"] = {
  trigger: "schedule",
  startedAt: new Date(Date.now() - 4 * 86_400_000).toISOString(),
  finishedAt: new Date(Date.now() - 4 * 86_400_000 + 9000).toISOString(),
  servers: [{ server: "This server", containers: 3, images: 7, volumes: 2, networks: 1, buildCache: 0, reclaimed: 1_850_000_000 }],
  deployments: 12,
};

export function cleanupView(): Cleanup {
  const c = db.cleanup;
  const view: Cleanup = {
    settings: c,
    running: false,
    nextRun: c.enabled ? new Date(Date.now() + 3 * 86_400_000).toISOString() : undefined,
    lastRun: cleanupLast,
  };
  if (cleanupStart === null) return view;
  const steps = (
    [
      ["connect", true],
      ["containers", c.containers],
      ["images", c.images !== "off"],
      ["buildcache", c.buildCache],
      ["volumes", true],
      ["networks", c.networks],
      ["deployments", c.keepDeployments > 0],
    ] as const
  ).filter(([, on]) => on);
  const at = Math.floor((Date.now() - cleanupStart) / 900);
  if (at < steps.length) return { ...view, running: true, step: steps[at][0], stepServer: steps[at][0] === "deployments" ? undefined : "This server" };
  cleanupLast = {
    trigger: "manual",
    startedAt: new Date(cleanupStart).toISOString(),
    finishedAt: now(),
    servers: [{ server: "This server", containers: c.containers ? 1 : 0, images: c.images !== "off" ? 2 : 0, volumes: 0, networks: 0, buildCache: 0, reclaimed: 412_000_000 }],
    deployments: c.keepDeployments > 0 ? 3 : 0,
  };
  cleanupStart = null;
  return { ...view, lastRun: cleanupLast };
}

export const routesC: Route[] = [
  [
    "POST",
    /^\/domains$/,
    ({ body }) => {
      const name = String(body.name ?? "").trim().toLowerCase();
      if (!/^([a-z0-9-]+\.)+[a-z]{2,}$/.test(name)) throw invalid(`domain "${name}" is not a valid hostname`);
      const d = { id: newId("dom"), name, proxied: false, createdAt: now() };
      db.domains.push(d);
      save();
      return d;
    },
  ],
  [
    "DELETE",
    /^\/domains\/([^/]+)$/,
    ({ m }) => {
      db.domains = db.domains.filter((d) => d.id !== m[1]);
      save();
    },
  ],
  [
    "GET",
    /^\/notifications$/,
    (): Notifications => ({
      channels: db.channels,
      kinds: [
        { name: "discord", label: "Discord", fields: [{ key: "webhookUrl", label: "Webhook URL", placeholder: "https://discord.com/api/webhooks/…", required: true, secret: true }] },
        {
          name: "email",
          label: "Email",
          fields: [
            { key: "to", label: "Recipients", placeholder: "ops@example.com, oncall@example.com", required: true, secret: false },
            { key: "provider", label: "Send through", required: true, secret: false, options: emailOptions() },
          ],
        },
        { name: "push", label: "Push", fields: [] },
      ],
      events: EVENTS,
      thresholds,
    }),
  ],
  ["PUT", /^\/notifications\/thresholds$/, ({ body }) => (thresholds = { ...thresholds, ...body })],
  [
    "POST",
    /^\/notifications\/channels$/,
    ({ body }) => {
      const kind = String(body.kind || "discord");
      const config: Record<string, string> =
        kind === "email" ? emailConfig(body.config as Record<string, string>) : kind === "push" ? {} : { webhookUrl: MASK };
      const ch = { id: newId("ch"), name: String(body.name || "Discord"), kind, config, events: body.events ?? [], enabled: body.enabled ?? true, createdAt: now() };
      db.channels.push(ch);
      save();
      return ch;
    },
  ],
  [
    "PUT",
    /^\/notifications\/channels\/([^/]+)$/,
    ({ m, body }) => {
      const ch = db.channels.find((x) => x.id === m[1]) ?? (() => { throw notFound(); })();
      Object.assign(ch, { name: body.name ?? ch.name, events: body.events ?? ch.events, enabled: body.enabled ?? ch.enabled });
      if (ch.kind === "email") ch.config = emailConfig({ ...ch.config, ...(body.config as Record<string, string>) });
      save();
      return ch;
    },
  ],
  [
    "DELETE",
    /^\/notifications\/channels\/([^/]+)$/,
    ({ m }) => {
      db.channels = db.channels.filter((c) => c.id !== m[1]);
      save();
    },
  ],
  ["POST", /^\/notifications\/channels\/([^/]+)\/test$/, () => undefined],
  ["GET", /^\/tokens$/, () => db.tokens],
  [
    "POST",
    /^\/tokens$/,
    ({ body }) => {
      const restricted = !!body.restricted;
      const t = {
        id: newId("tok"),
        name: String(body.name || "token"),
        restricted,
        features: restricted ? body.features || [] : [],
        grants: restricted ? body.grants || {} : {},
        createdAt: now(),
      };
      db.tokens.push(t);
      save();
      return { ...t, token: `kpt_demo_${Math.random().toString(36).slice(2)}${Math.random().toString(36).slice(2)}` };
    },
  ],
  [
    "DELETE",
    /^\/tokens\/([^/]+)$/,
    ({ m }) => {
      db.tokens = db.tokens.filter((t) => t.id !== m[1]);
      save();
    },
  ],
  ["GET", /^\/audit$/, () => db.audit],
  ["GET", /^\/users$/, () => users()],
  [
    "PUT",
    /^\/users\/([^/]+)$/,
    ({ m, body }) => {
      const u = users().find((u) => u.id === m[1]);
      if (!u) throw new HttpError(404, "not found");
      if (u.id === "demo" && body.role !== "admin") throw invalid("this is the last admin; make another user admin first");
      Object.assign(u, body.role === "admin" ? { role: "admin", features: [], grants: {} } : { role: "member", features: body.features ?? [], grants: body.grants ?? {} });
      save();
      return u;
    },
  ],
  [
    "DELETE",
    /^\/users\/([^/]+)$/,
    ({ m }) => {
      if (m[1] === "demo") throw invalid("you can't delete your own account");
      db.users = users().filter((u) => u.id !== m[1]);
      save();
    },
  ],
  ["GET", /^\/invites$/, () => (db.invites ??= [])],
  [
    "POST",
    /^\/invites$/,
    ({ body }) => {
      const admin = body.role === "admin";
      const i = {
        id: newId("inv"),
        role: admin ? ("admin" as const) : ("member" as const),
        features: admin ? [] : (body.features ?? []),
        grants: admin ? {} : (body.grants ?? {}),
        createdBy: "demo",
        ...(body.email ? { email: String(body.email) } : {}),
        createdAt: now(),
        expiresAt: new Date(Date.now() + 7 * 86_400_000).toISOString(),
      };
      if (body.email && !db.emailProviders?.length) throw invalid("email isn't set up; add an email provider in Settings › Email");
      (db.invites ??= []).push(i);
      save();
      return { ...i, token: `demo${Math.random().toString(36).slice(2)}${Math.random().toString(36).slice(2)}` };
    },
  ],
  [
    "DELETE",
    /^\/invites\/([^/]+)$/,
    ({ m }) => {
      db.invites = (db.invites ?? []).filter((i) => i.id !== m[1]);
      save();
    },
  ],
  ["GET", /^\/analytics$/, () => analyticsSettings],
  [
    "PUT",
    /^\/analytics$/,
    ({ body, q }) => {
      const turnedOn = body.enabled && !analyticsSettings.enabled;
      analyticsSettings = { ...analyticsSettings, ...body };
      return q.has("progress") ? progressed(turnedOn ? ["image", "start", "health", "sites"] : ["sites"], analyticsSettings) : analyticsSettings;
    },
  ],
  ["DELETE", /^\/analytics\/data$/, () => undefined],
  ["GET", /^\/analytics\/trackers$/, () => trackers.map(trackerURL)],
  [
    "POST",
    /^\/analytics\/trackers$/,
    ({ body }) => {
      const t: Tracker = { ...body, id: `trk-${Date.now()}`, slug: body.slug || Math.random().toString(36).slice(2, 10), createdAt: Date.now() / 1000, hits: 0, url: "" };
      trackers = [t, ...trackers];
      return trackerURL(t);
    },
  ],
  [
    "PUT",
    /^\/analytics\/trackers\/([^/]+)$/,
    ({ m, body }) => {
      trackers = trackers.map((t) => (t.id === m[1] ? { ...t, name: body.name, slug: body.slug || t.slug, target: body.target } : t));
      return trackerURL(trackers.find((t) => t.id === m[1])!);
    },
  ],
  ["DELETE", /^\/analytics\/trackers\/([^/]+)$/, ({ m }) => void (trackers = trackers.filter((t) => t.id !== m[1]))],
  [
    "GET",
    /^\/analytics\/trackers\/([^/]+)\/stats$/,
    ({ m, q }) => ({
      ...demoAnalytics(Number(q.get("from")), Number(q.get("to")), Number(q.get("interval"))),
      tracker: trackerURL(trackers.find((t) => t.id === m[1])!),
    }),
  ],
  ["GET", /^\/services\/([^/]+)\/analytics\/visits$/, ({ q }) => (q.get("before") ? [] : demoVisits(Number(q.get("to"))))],
  ["GET", /^\/services\/([^/]+)\/analytics\/visits\/([^/]+)$/, ({ m }) => demoVisit(m[2])],
  [
    "GET",
    /^\/services\/([^/]+)\/analytics\/funnel$/,
    ({ q }) => q.getAll("step").map((step, i) => ({ step, visits: Math.round(1200 * 0.45 ** i) })),
  ],
  [
    "POST",
    /^\/services\/([^/]+)\/analytics\/share$/,
    ({ m }) => ((serviceOf(m[1]).analyticsSharedAt = new Date().toISOString()), save(), { token: "demo-share-link" }),
  ],
  ["DELETE", /^\/services\/([^/]+)\/analytics\/share$/, ({ m }) => void (delete serviceOf(m[1]).analyticsSharedAt, save())],
  [
    "GET",
    /^\/share\/analytics\/([^/]+)$/,
    ({ q }) => ({ ...demoAnalytics(Number(q.get("from")), Number(q.get("to")), Number(q.get("interval"))), domain: "acme.example", script: undefined }),
  ],
  ["GET", /^\/services\/([^/]+)\/errors$/, ({ q }) => demoIssues(q)],
  ["GET", /^\/services\/([^/]+)\/errors\/series$/, ({ q }) => demoErrorSeries(q)],
  ["GET", /^\/services\/([^/]+)\/errors\/deploy$/, ({ m }) => demoDeployErrors(db.deployments.filter((d) => d.serviceId === m[1]))],
  ["GET", /^\/services\/([^/]+)\/errors\/(\d+)$/, ({ m }) => demoIssue(m[2])],
  ["GET", /^\/services\/([^/]+)\/errors\/(\d+)\/event$/, ({ m }) => demoIssueEvent(m[2])],
  ["PUT", /^\/services\/([^/]+)\/errors\/(\d+)\/status$/, ({ m, body }) => setDemoIssueStatus(m[2], body.status)],
  ["GET", /^\/services\/([^/]+)\/analytics$/, ({ q }) => demoAnalytics(Number(q.get("from")), Number(q.get("to")), Number(q.get("interval")))],
  ["GET", /^\/cleanup$/, () => cleanupView()],
  [
    "PUT",
    /^\/cleanup$/,
    ({ body }) => {
      db.cleanup = { ...db.cleanup, ...body };
      save();
      return cleanupView();
    },
  ],
  ["POST", /^\/cleanup\/run$/, () => ((cleanupStart ??= Date.now()), cleanupView())],
  ["GET", /^\/storage$/, () => [{ id: "local", name: "Local disk", kind: "local", config: {}, createdAt: now() }]],
  ["POST", /^\/storage\/([^/]+)\/test$/, ({ q }) => (q.has("progress") ? progressed(["write", "delete"], undefined) : undefined)],
  [
    "GET",
    /^\/services\/([^/]+)\/connection$/,
    ({ m }): Connection => {
      const s = serviceOf(m[1]);
      const d = dbDemo[s.kind] ?? dbDemo.postgres;
      return { host: s.name, port: d.port, database: d.database, user: d.user, password: "demo-password", url: d.url(s.name, "demo-password") };
    },
  ],

  [
    "GET",
    /^\/services\/([^/]+)\/data\/databases$/,
    ({ m }) => {
      const kind = serviceOf(m[1]).kind;
      // An app's databases are the SQLite files found in its volumes.
      if (kind === "app") return [{ name: "data/app.db", bytes: 2_097_152, main: false }];
      return [kind === "postgres" ? { name: "postgres", bytes: 9_437_184, main: true } : { name: "app", bytes: 9_437_184, main: true }];
    },
  ],
];
