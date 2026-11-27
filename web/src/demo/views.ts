import { MASK } from "./state";
import type { AnalyticsStats, AnalyticsVisit, AnalyticsVisitDetail, Tracker, Backup, Schedule, PreviewSettings, AnonymizeRules, SeedDatabase, UpgradeNotice } from "@/api";
import { files } from "./files";
import { rng, type DemoDb } from "./seed";

import { backupKindOf } from "./ops";
import { analyticsSettings } from "./routes-c";
import { HttpError, db, isDb, newId, now, projectOf } from "./state";

export function newBackup(s: DemoDb["services"][number] | null, targetId = "local"): Backup {
  const p = s ? projectOf(s.projectId) : null;
  const kind = !s ? "manager" : backupKindOf(s.kind);
  const t = now();
  const size = kind === "postgres" ? 4_400_000 + Math.floor(Math.random() * 200_000) : kind === "manager" ? 530_000 : 190_000;
  return {
    id: newId("bk"),
    kind,
    database: kind === "postgres" ? "postgres" : undefined,
    encrypted: false,
    serviceId: s?.id ?? "",
    projectId: p?.id ?? "",
    serviceName: s?.name ?? "",
    projectName: p?.name ?? "",
    serviceKind: s?.kind,
    serviceIcon: s ? s.icon || (isDb(s) ? s.kind : "") : undefined,
    targetId,
    objectKey: s
      ? `${p!.name}/${s.name}/${t.slice(0, 19)}${kind === "postgres" ? ".dump" : kind === "volume" ? ".tar.gz" : s.kind === "mongodb" ? ".archive.gz" : ".sql.gz"}`
      : `manager/${t.slice(0, 19)}.db`,
    status: "succeeded",
    sizeBytes: size,
    sha256: Math.random().toString(16).slice(2, 10) + "e3b0c44298fc1c149afbf4c8996fb924",
    pgVersion: kind === "postgres" ? "17.6" : kind === "dump" ? (s!.image.split(":")[1] ?? "") : "",
    volumes: kind === "volume" ? [`kipitiny-${s!.id}-data`] : undefined,
    durationMs: kind === "volume" ? 700 : 2100,
    createdAt: t,
    finishedAt: t,
    verifyDetails: { tables: 0, rows: 0, dbBytes: 0, durationMs: 0 },
  };
}

/** A dozen recent visits; the demo's journeys are made up from their id. */
export function demoVisits(to: number): AnalyticsVisit[] {
  const r = rng(7);
  const pages = ["/", "/pricing", "/blog/launch", "/docs", "/signup"];
  return Array.from({ length: 12 }, (_, i) => {
    const startedAt = to - 600 - i * 2700;
    const pageviews = 1 + Math.floor(r() * 5);
    return {
      id: `visit-${i}`,
      startedAt,
      lastAt: startedAt + (pageviews - 1) * 70,
      pageviews,
      events: i % 3 === 0 ? 1 : 0,
      entry: pages[i % 3],
      exit: pages[(i + pageviews) % pages.length],
      referrer: ["google.com", "", "news.ycombinator.com", "github.com"][i % 4],
      utmSource: i % 5 === 1 ? "newsletter" : "",
      utmMedium: "",
      utmCampaign: "",
      country: ["FR", "US", "DE", "GB"][i % 4],
      browser: ["Chrome", "Safari", "Firefox"][i % 3],
      os: ["macOS", "Windows", "iOS"][i % 3],
      device: ["desktop", "desktop", "mobile"][i % 3],
      screen: ["1920x1080", "1440x900", "390x844"][i % 3],
      language: "en-us",
      identity: i === 0 ? "ada@acme.example" : "",
      identityProps: i === 0 ? { plan: "pro" } : undefined,
    };
  });
}

export function demoVisit(id: string): AnalyticsVisitDetail {
  const v = demoVisits(Math.ceil(Date.now() / 60000) * 60).find((v) => v.id === id) ?? demoVisits(0)[0];
  const pages = ["/", "/pricing", "/docs", "/signup", "/blog/launch"];
  const journey: AnalyticsVisitDetail["journey"] = Array.from({ length: v.pageviews }, (_, i) => ({
    at: v.startedAt + i * 70,
    name: "",
    path: i === 0 ? v.entry : pages[i % pages.length],
    query: "",
    title: "Acme",
  }));
  if (v.events) journey.push({ at: v.lastAt, name: "signup", path: "/signup", query: "", title: "Acme", props: { plan: "pro" } });
  return { ...v, journey };
}

export const trackerURL = (t: Tracker) =>
  ({ ...t, url: `https://${analyticsSettings.domain}/${t.kind === "pixel" ? `p/${t.slug}.gif` : `l/${t.slug}`}` }) as Tracker;

/** Plausible visits of the demo's site, the same for everyone. */
export function demoAnalytics(from: number, to: number, interval: number): AnalyticsStats {
  const r = rng(from / interval);
  const series = [];
  for (let t = Math.floor(from / interval) * interval; t < to; t += interval) {
    const hour = new Date(t * 1000).getHours();
    const visitors = Math.round((interval >= 86400 ? 140 : 4 + 8 * Math.sin((Math.PI * hour) / 24)) * (0.6 + r()));
    series.push({ t, visitors, pageviews: Math.round(visitors * (1.8 + r())) });
  }
  const visitors = series.reduce((n, p) => n + p.visitors, 0);
  const pageviews = series.reduce((n, p) => n + p.pageviews, 0);
  const rows = (values: string[]) => {
    let left = visitors;
    return values.map((value) => {
      const n = Math.max(1, Math.round(left * (0.35 + r() * 0.2)));
      left -= n;
      return { value, visitors: n, count: Math.round(n * (1.2 + r())) };
    });
  };
  return {
    pageviews,
    visitors,
    previous: {
      pageviews: Math.round(pageviews * 0.9),
      visitors: Math.round(visitors * 1.05),
      visits: Math.round(visitors * 1.25),
      bounces: Math.round(visitors * 0.6),
      duration: Math.round(visitors * 1.25 * 88),
    },
    visits: Math.round(visitors * 1.3),
    bounces: Math.round(visitors * 0.55),
    duration: Math.round(visitors * 1.3 * 95),
    active: 3,
    series,
    breakdowns: {
      pages: rows(["/", "/pricing", "/blog/launch", "/docs", "/signup"]),
      entries: rows(["/", "/blog/launch", "/pricing"]),
      exits: rows(["/", "/signup", "/pricing"]),
      referrers: rows(["google.com", "news.ycombinator.com", "github.com", "duckduckgo.com"]),
      countries: rows(["FR", "US", "DE", "GB", "CA"]),
      languages: rows(["en-us", "fr-fr", "de-de"]),
      browsers: rows(["Chrome", "Safari", "Firefox", "Edge"]),
      os: rows(["macOS", "Windows", "iOS", "Android", "Linux"]),
      devices: rows(["desktop", "mobile", "tablet"]),
      screens: rows(["1920x1080", "1440x900", "390x844"]),
      utm_source: rows(["newsletter", "twitter"]),
      events: rows(["signup", "download"]),
      identities: rows(["ada@acme.example", "grace@acme.example"]),
      props: rows(["signup: plan=pro", "signup: plan=free"]),
    },
    script: `<script defer src="https://${analyticsSettings.domain}/a.js"></script>`,
  };
}

export const scheduleFrom = (body: any, base: Partial<Schedule>): Schedule => ({ // eslint-disable-line @typescript-eslint/no-explicit-any
  id: base.id ?? newId("sch"),
  kind: base.kind ?? "postgres",
  serviceId: base.serviceId,
  createdAt: base.createdAt ?? now(),
  nextRun: new Date(Date.now() + 86_400_000).toISOString(),
  targetId: body.targetId || "local",
  database: body.database,
  cron: body.cron || "0 3 * * *",
  keepLast: Number(body.keepLast) || 0,
  keepDaily: Number(body.keepDaily) || 0,
  keepWeekly: Number(body.keepWeekly) || 0,
  keepMonthly: Number(body.keepMonthly) || 0,
  enabled: body.enabled ?? true,
  verify: body.verify ?? true,
});

/** The project (or one service) as a docker-compose file, like the manager's export. */
export function compose(projectId: string, only?: string): string {
  const p = projectOf(projectId);
  const q = (v: string) => (/^[\w./:-]+$/.test(v) ? v : JSON.stringify(v));
  const lines = [`name: ${p.name}`, "services:"];
  for (const s of db.services.filter((x) => x.projectId === p.id && (!only || x.name === only))) {
    lines.push(`  ${s.name}:`, `    image: ${s.image}`);
    const env = Object.entries(s.env).filter(() => !isDb(s));
    if (env.length) {
      lines.push("    environment:");
      for (const [k, v] of env) lines.push(`      ${k}: ${s.secrets.includes(k) ? `\${${k}}` : q(v)}`);
    }
    if (s.port && !s.domain) lines.push("    expose:", `      - "${s.port}"`);
    if (s.replicas > 1 || s.memoryMb) {
      lines.push("    deploy:");
      if (s.replicas > 1) lines.push(`      replicas: ${s.replicas}`);
      if (s.memoryMb) lines.push("      resources:", "        limits:", `          memory: ${s.memoryMb}M`);
    }
    const x: [string, string | number][] = [];
    if (isDb(s)) x.push(["kind", s.kind]);
    if (s.domain) x.push(["domain", s.domain], ["port", s.port]);
    if (s.healthPath) x.push(["health_path", s.healthPath]);
    if (s.preDeploy) x.push(["pre_deploy", q(s.preDeploy)]);
    if (x.length) lines.push("    x-kipitiny:", ...x.map(([k, v]) => `      ${k}: ${v}`));
  }
  return lines.join("\n") + "\n";
}

/** Runs a volume files fake, its errors as HTTP ones. */
export function viaFiles<T>(f: () => T): T {
  try {
    return f();
  } catch (e) {
    if (e instanceof files.FilesError) throw new HttpError(e.status, e.message);
    throw e;
  }
}

/** What the fake new version would do to running servers. */
export const demoNotices: UpgradeNotice[] = [
  { version: "demo-next", level: "warning", text: "Traefik restarts once on every server: apps are unreachable for a few seconds. Upgrade at a quiet time." },
];

/** Preview settings per project, in memory only. */
export const demoPreviewSettings = new Map<string, PreviewSettings>();

export const demoSeedRules = new Map<string, AnonymizeRules | null>([["db", { users: { email: "fake:email" }, sessions: "truncate" }]]);

/** The project's databases, each with the copy of its latest backup when it has rules. */
export function previewSeed(projectId: string): SeedDatabase[] {
  return db.services
    .filter((s) => s.projectId === projectId && ["postgres", "mysql", "mariadb", "mongodb"].includes(s.kind))
    .map((s) => {
      const backup = db.backups.find((b) => b.serviceId === s.id && b.status === "succeeded");
      const rules = demoSeedRules.get(s.name) ?? null;
      const copy = backup && rules ? { ...backup, id: backup.id + "-anon", sourceId: backup.id, objectKey: backup.objectKey + ".anon" } : undefined;
      return { serviceId: s.id, name: s.name, kind: s.kind, rules, backup, copy, seeds: copy };
    });
}

export function previewSettings(projectId: string): PreviewSettings {
  const s = demoPreviewSettings.get(projectId) ?? {
    baseDomain: projectId === "acme" ? "preview.acme.example" : "",
    env: projectId === "acme" ? { STRIPE_KEY: "sk_test_demo", LOG_LEVEL: "debug" } : {},
    secrets: projectId === "acme" ? ["STRIPE_KEY"] : [],
    inherit: [],
    ttlSeconds: 0,
    sleepAfterSeconds: 0,
    seed: projectId === "acme" ? "anonymized" : "",
  };
  return { ...s, env: Object.fromEntries(Object.entries(s.env).map(([k, v]) => [k, s.secrets.includes(k) ? MASK : v])) };
}
