import type { Backup, Restore, Notifications, SecretConnection, SecretProvider, Deployment, Uptime, UptimeResult } from "@/api";
import { type DemoDb } from "./seed";
import { db, newId, notFound, now, save } from "./state";

export type Ctx = { m: RegExpMatchArray; q: URLSearchParams; body: any }; // eslint-disable-line @typescript-eslint/no-explicit-any

export type Route = [method: string, path: RegExp, handler: (c: Ctx) => unknown];

export const EVENTS: Notifications["events"] = [
  { type: "deploy.failed", label: "Deployment failed", default: true },
  { type: "deploy.succeeded", label: "Deployment succeeded", default: false },
  { type: "git.sync.failed", label: "Git sync failed", default: true },
  { type: "git.sync.succeeded", label: "Git sync applied changes", default: false },
  { type: "service.restarted", label: "Stopped service restarted", default: true },
  { type: "service.unhealthy", label: "Service unhealthy", default: true },
  { type: "service.healthy", label: "Service healthy again", default: true },
  { type: "service.oom_killed", label: "Service ran out of memory", default: true },
  { type: "service.memory_high", label: "Service memory high", default: true },
  { type: "service.memory_ok", label: "Service memory back to normal", default: true },
  { type: "service.cpu_high", label: "Service CPU high", default: true },
  { type: "service.cpu_ok", label: "Service CPU back to normal", default: true },
  { type: "uptime.down", label: "Uptime check failing", default: true },
  { type: "uptime.up", label: "Uptime check recovered", default: true },
  { type: "job.failed", label: "Scheduled job failed", default: true },
  { type: "job.succeeded", label: "Scheduled job succeeded", default: false },
  { type: "backup.failed", label: "Backup failed", default: true },
  { type: "backup.succeeded", label: "Backup succeeded", default: false },
  { type: "verify.failed", label: "Restore test failed", default: true },
  { type: "verify.succeeded", label: "Restore test passed", default: false },
  { type: "restore.failed", label: "Restore failed", default: true },
  { type: "restore.succeeded", label: "Restore succeeded", default: true },
  { type: "cleanup.failed", label: "Cleanup had errors", default: true },
  { type: "update.available", label: "New kipitiny version", default: true },
  { type: "server.updates_pending", label: "Server has security updates", default: true },
  { type: "server.reboot_required", label: "Server needs a reboot", default: true },
  { type: "server.disk_low", label: "Server disk almost full", default: true },
  { type: "server.disk_ok", label: "Server disk space recovered", default: true },
];

export const backupKindOf = (kind: string) => (kind === "postgres" ? "postgres" : kind === "redis" || kind === "app" ? "volume" : "dump");

/** A fresh backup of a service (or the manager), as if it just ran. */
/** The demo's password manager: Proton Pass, whose CLI the real manager fetches on first use. */
export const passwordManager = (): SecretProvider => ({
  id: "protonpass",
  name: "Proton Pass",
  scheme: "pass",
  tokenLabel: "Personal access token",
  example: "pass://Vault/Item/password",
  help: "This is a demo: any token connects, to sample vaults. On a server, create one with `pass-cli personal-access-token create --name kipitiny --expiration 1y`.",
  available: true,
});

/** The demo's password manager connections, seeded from the managers connected before there were several. */
export const secretConnections = (): SecretConnection[] =>
  (db.secretConnections ??= db.passwordManagers.map((kind) => ({
    id: `sc-${kind}`,
    kind,
    name: "",
    scheme: "pass",
    providerName: "Proton Pass",
    createdAt: new Date().toISOString(),
  })));

/** The demo's Proton Pass helper: pulled once per page load, running for 5 minutes after each use. */
export const helper = { pulled: false, until: 0 };

/** What the manager would do first: pull the CLI if missing, start its helper if idle. */
export const helperSteps = () => {
  const steps = [...(helper.pulled ? [] : ["image"]), ...(Date.now() < helper.until ? [] : ["helper"])];
  helper.pulled = true;
  helper.until = Date.now() + 5 * 60_000;
  return steps;
};

/** A ?progress answer: each step after a beat of work, as NDJSON, then the result (an Error ends it failed). */
export const progressed = (steps: string[], result: unknown) =>
  new Response(
    new ReadableStream({
      async start(c) {
        const line = (v: unknown) => c.enqueue(new TextEncoder().encode(JSON.stringify(v) + "\n"));
        for (const step of steps) {
          line({ step });
          await new Promise((r) => setTimeout(r, step === "image" ? 1800 : 700 + Math.random() * 400));
        }
        line(result instanceof Error ? { error: result.message } : { result: result ?? null });
        c.close();
      },
    }),
    { headers: { "Content-Type": "application/x-ndjson" } },
  );

/** Backups and restores the demo is running, by ID: when each started and the steps it walks through. */
export const runs = new Map<string, { start: number; steps: [string, number][] }>();

/** Where a run is: its current step and how far into it (0–1), or null once it's over (and forgotten). */
export function runStep(id: string): { step: string; progress: number } | null {
  const run = runs.get(id);
  if (!run) return null;
  let t = Date.now() - run.start;
  for (const [step, ms] of run.steps) {
    if (t < ms) return { step, progress: t / ms };
    t -= ms;
  }
  runs.delete(id);
  return null;
}

/** Starts b as a running backup the demo walks through its steps. */
export function startBackup(b: Backup): Backup {
  const upload = b.kind === "postgres" ? 3200 : 2200;
  const steps: [string, number][] =
    b.kind === "manager" ? [["snapshot", 900], ["upload", upload]] : b.kind === "volume" ? [["helper", 1100], ["upload", upload]] : [["prepare", 900], ["upload", upload]];
  runs.set(b.id, { start: Date.now(), steps });
  return Object.assign(b, { status: "running" as const, finishedAt: undefined });
}

/** A backup as it stands: its step and bytes while running, done once its steps are. */
export function backingUp(b: Backup): Backup {
  if (b.status !== "running") return b;
  const at = runStep(b.id);
  if (at) return { ...b, step: at.step as Backup["step"], stepBytes: at.step === "upload" ? Math.round(b.sizeBytes * at.progress) : undefined };
  Object.assign(b, { status: "succeeded", finishedAt: now() });
  save();
  return b;
}

/** A restore as it stands, like backingUp. */
export function restoring(r: Restore): Restore {
  if (r.status !== "running") return r;
  const at = runStep(r.id);
  if (at) return { ...r, step: at.step as Restore["step"] };
  Object.assign(r, { status: "succeeded", finishedAt: now() });
  save();
  return r;
}

/** Deployments an install started, as the manager runs them: each starts at `start` and ends at `end` (ms). */
export const scheduled: { serviceId: string; start: number; end: number; id?: string }[] = [];

/** Starts the scheduled deployments whose time came and finishes those whose time is up. */
export function tickDeploys() {
  const t = Date.now();
  for (const x of scheduled) {
    const s = db.services.find((s) => s.id === x.serviceId);
    if (!s) continue;
    if (!x.id && t >= x.start) {
      const d: Deployment = { id: newId("dep"), serviceId: s.id, status: "running", image: s.image, triggeredBy: "user:demo", createdAt: now() };
      db.deployments.unshift(d);
      x.id = d.id;
    }
    const d = x.id && db.deployments.find((d) => d.id === x.id);
    if (d && d.status === "running" && t >= x.end) {
      Object.assign(d, { status: "succeeded", finishedAt: now() });
      s.currentDeploymentId = d.id;
    }
  }
  scheduled.splice(0, scheduled.length, ...scheduled.filter((x) => !x.id || db.deployments.find((d) => d.id === x.id)?.status === "running"));
  save();
}

/** Restore tests the demo is running, by backup: when each started. */
export const verifyStarts = new Map<string, number>();

/** A backup as its restore test stands: walks through the steps a real one takes, then passes. */
export function verifying(b: Backup): Backup {
  const start = verifyStarts.get(b.id);
  if (start === undefined) return b;
  const steps: [NonNullable<Backup["verifyStep"]>, number][] = b.kind === "volume" ? [["server", 1000], ["read", 2200]] : [["image", 1200], ["server", 2200], ["restore", 2400], ["check", 900]];
  let t = Date.now() - start;
  for (const [step, ms] of steps) {
    if (t < ms) return { ...b, verifyStep: step };
    t -= ms;
  }
  verifyStarts.delete(b.id);
  Object.assign(b, {
    verifyStatus: "succeeded",
    verifiedAt: now(),
    verifyDetails: b.kind !== "volume" ? { tables: db.tables.length, rows: db.tables.reduce((n, t) => n + t.rows.length, 0), dbBytes: b.sizeBytes * 3, durationMs: 6700 } : { tables: 0, rows: 0, dbBytes: 0, files: 3, durationMs: 3200 },
  });
  save();
  return b;
}

export const connection = (id: string) => {
  const c = secretConnections().find((c) => c.id === id);
  if (!c) throw notFound();
  return c;
};

/** The sample vaults: item titles and the fields a reference can name. */
export const vaults: Record<string, [string, string[]][]> = {
  Production: [
    ["Stripe", ["secret_key", "publishable_key", "webhook_secret"]],
    ["Postmark", ["server_token"]],
    ["Sentry", ["dsn", "auth_token"]],
    ["S3 backups", ["access_key_id", "secret_access_key"]],
    ["GitHub deploy bot", ["username", "password", "totp"]],
  ],
  Staging: [
    ["Stripe test", ["secret_key", "publishable_key", "webhook_secret"]],
    ["Postmark sandbox", ["server_token"]],
  ],
};

/**
 * An uptime check's record, made up from the clock: a fast, steady response with the odd slow one, and a short
 * outage around the api's last deploy so the bars show what a failure looks like.
 */
/** A pseudo-random number in [0, 1) that only depends on n. */
export const rand = (n: number) => {
  const x = Math.sin(n * 12.9898) * 43758.5453;
  return x - Math.floor(x);
};

export function uptime(s: DemoDb["services"][number]): Uptime {
  const chk = db.uptime.find((c) => c.serviceId === s.id) ?? null;
  if (!chk) return { check: null, recent: [], uptime24h: null, uptime7d: null, uptime30d: null, avgLatencyMs: null };
  const url = s.domain ? `https://${s.domain}${chk.path}` : undefined;
  const problem = s.stopped ? "the service is stopped" : !chk.enabled ? "paused" : undefined;
  const step = chk.intervalSec * 1000;
  const last = Math.floor(Date.now() / step) * step;
  const base = s.id === "api" ? 42 : 18;
  const recent: UptimeResult[] = Array.from({ length: 60 }, (_, i) => {
    const t = last - (59 - i) * step;
    // Same numbers for the same minute, whatever the refresh.
    const r = rand(t / step + base);
    if (s.id === "api" && (i === 37 || i === 38)) return { at: new Date(t).toISOString(), ok: false, status: 502, latencyMs: 12, error: "502 Bad Gateway" };
    return { at: new Date(t).toISOString(), ok: true, status: 200, latencyMs: Math.round(base * (0.8 + 0.5 * r + (r > 0.94 ? 2.5 : 0))) };
  });
  const passed = recent.filter((r) => r.ok);
  const fresh = s.id === "api";
  return {
    check: fresh ? { ...chk, changedAt: recent[39].at } : chk,
    url,
    problem,
    recent,
    uptime24h: fresh ? 99.86 : 100,
    uptime7d: fresh ? 99.97 : 100,
    uptime30d: fresh ? 99.99 : 99.98,
    avgLatencyMs: Math.round(passed.reduce((n, r) => n + r.latencyMs, 0) / passed.length),
  };
}
