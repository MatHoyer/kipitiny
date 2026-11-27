import type { Container, Project, TagInfo, Server, Service, Job, JobInput, JobRun } from "@/api";
import { autoTagColor, normalizeTag } from "@/lib/tags";
import { demoJobs, demoUsers, seed, type DemoDb } from "./seed";

export const KEY = "kipitiny-demo";

export function load(): DemoDb {
  try {
    const raw = localStorage.getItem(KEY);
    if (raw) {
      const db = JSON.parse(raw) as DemoDb;
      if (db.version === 3) return db;
    }
  } catch {
    // Private mode or a broken value: start over.
  }
  return seed();
}

export let db = load();

export const save = () => {
  try {
    localStorage.setItem(KEY, JSON.stringify(db));
  } catch {
    // Storage blocked: the demo works, it just won't persist.
  }
};

export function resetDemo() {
  try {
    localStorage.removeItem(KEY);
  } catch {
    // Nothing to remove.
  }
  db = seed();
}

export class HttpError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

export const notFound = () => new HttpError(404, "not found");

export const invalid = (m: string) => new HttpError(400, m);

export const now = () => new Date().toISOString();

export const users = () => (db.users ??= demoUsers());

export const newId = (prefix: string) => `${prefix}-${Math.random().toString(36).slice(2, 10)}`;

export const nameRe = /^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/;

/** Validates tags as the manager does (core.normalizeTags). */
export function demoTags(input: unknown): string[] {
  const out: string[] = [];
  for (const raw of Array.isArray(input) ? input : []) {
    const t = normalizeTag(String(raw));
    if (!t || out.includes(t)) continue;
    if ([...t].length > 32 || !/^[\p{L}\p{N}][\p{L}\p{N} ._/:-]*$/u.test(t)) throw invalid(`tag "${t}" must be up to 32 letters, digits, spaces and . _ / : -`);
    out.push(t);
  }
  if (out.length > 10) throw invalid("up to 10 tags");
  return out.sort();
}

/** The tags in use, with their colors. */
export function demoTagList(): TagInfo[] {
  const byName = new Map<string, TagInfo>();
  const get = (name: string) => {
    if (!byName.has(name)) {
      const custom = db.tagColors?.[name];
      byName.set(name, { name, color: custom ?? autoTagColor(name), custom: !!custom, projects: 0, services: 0 });
    }
    return byName.get(name)!;
  };
  for (const p of db.projects) for (const t of p.tags ?? []) get(t).projects++;
  for (const s of db.services) for (const t of s.tags ?? []) get(t).services++;
  return [...byName.values()].sort((a, b) => a.name.localeCompare(b.name));
}

export const MASK = "********";

/** The demo's email providers, passwords masked, and the one invites use: the chosen one, else the oldest. */
export const emailView = () => {
  const ps = db.emailProviders ?? [];
  return {
    providers: ps.map((p) => ({ ...p, password: p.password && MASK })),
    invites: ps.find((p) => p.id === db.emailInvites)?.id ?? ps[0]?.id ?? "",
  };
};

/** The email providers an email channel can send through, the invites one first. */
export const emailOptions = () => {
  const { providers, invites } = emailView();
  return [...providers].sort((a, b) => +(b.id === invites) - +(a.id === invites)).map((p) => ({ value: p.id, label: p.name }));
};

/** An email channel's config, checked like the manager does. */
export const emailConfig = (c: Record<string, string> | undefined) => {
  if (!db.emailProviders?.some((p) => p.id === c?.provider)) throw invalid("unknown email provider; add one in Settings › Email");
  return { to: String(c?.to ?? ""), provider: String(c?.provider) };
};

export const SERVER: Server = {
  id: "local",
  name: "This server",
  kind: "local",
  host: "",
  port: 22,
  sshUser: "",
  socket: "/var/run/docker.sock",
  hostKey: "",
  projects: 0,
  docker: { version: "27.3.1", os: "linux", arch: "amd64" },
  publicIp: "",
  detectedIp: "203.0.113.10",
} as Server;

export const projectOf = (id: string) => db.projects.find((p) => p.id === id) ?? (() => { throw notFound(); })();

/** The jobs and their runs; a demo run takes 3 seconds and succeeds. */
export function jobState() {
  if (!db.jobs || !db.jobRuns) ({ jobs: db.jobs, runs: db.jobRuns } = demoJobs());
  for (const r of db.jobRuns!) {
    if (r.status === "running" && Date.now() - Date.parse(r.createdAt) > 3_000) {
      Object.assign(r, { status: "succeeded", exitCode: 0, finishedAt: new Date(Date.parse(r.createdAt) + 3_000).toISOString() });
    }
  }
  return { jobs: db.jobs!, runs: db.jobRuns! };
}

export function jobView(j: Job): Job {
  return { ...j, lastRun: jobState().runs.find((r) => r.jobId === j.id), nextRun: j.enabled ? j.nextRun : undefined };
}

export function jobFrom(body: unknown, base: Partial<Job>): Job {
  const b = body as JobInput;
  if (!nameRe.test(b.name ?? "")) throw invalid("job name must be lowercase letters, digits and dashes");
  if (!b.command?.trim()) throw invalid("a job needs a command");
  if (!b.cron?.trim()) throw invalid("schedule: empty spec string");
  const { jobs } = jobState();
  if (jobs.some((j) => j.serviceId === base.serviceId && j.name === b.name && j.id !== base.id)) {
    throw invalid(`the service already has a job named ${b.name}`);
  }
  return {
    id: base.id ?? newId("job"),
    serviceId: base.serviceId!,
    createdAt: base.createdAt ?? now(),
    nextRun: base.nextRun ?? new Date(Date.now() + 3_600_000).toISOString(),
    name: b.name,
    command: b.command.trim(),
    cron: b.cron.trim(),
    timeoutSec: b.timeoutSec || 3600,
    enabled: b.enabled,
  };
}

export const took = (r: JobRun) => `${Math.round((Date.parse(r.finishedAt!) - Date.parse(r.createdAt)) / 1000)}s`;

export const jobOf = (id: string) => jobState().jobs.find((j) => j.id === id) ?? (() => { throw notFound(); })();

export const serviceOf = (id: string) => db.services.find((s) => s.id === id) ?? (() => { throw notFound(); })();

export const isDb = (s: { kind: string }) => s.kind !== "app";

/** How the demo's databases are reached, per kind. */
export const dbDemo: Record<string, { image: string; port: number; url: (name: string, password: string) => string; user: string; database?: string }> = {
  postgres: { image: "postgres:18-alpine", port: 5432, user: "app", database: "postgres", url: (n, pw) => `postgres://app:${pw}@${n}:5432/postgres` },
  redis: { image: "redis:8-alpine", port: 6379, user: "", url: (n, pw) => `redis://:${pw}@${n}:6379` },
  mysql: { image: "mysql:8.4", port: 3306, user: "app", database: "app", url: (n, pw) => `mysql://app:${pw}@${n}:3306/app` },
  mariadb: { image: "mariadb:11.8", port: 3306, user: "app", database: "app", url: (n, pw) => `mysql://app:${pw}@${n}:3306/app` },
  mongodb: { image: "mongo:8.2", port: 27017, user: "app", database: "app", url: (n, pw) => `mongodb://app:${pw}@${n}:27017/app?authSource=admin` },
};

/** A short, stable fake container ID. */
export const hash = (s: string) => {
  let h = 2166136261;
  for (const c of s) h = Math.imul(h ^ c.charCodeAt(0), 16777619);
  return (h >>> 0).toString(16).padStart(8, "0") + "a1b2";
};

export function containers(s: DemoDb["services"][number]): Container[] {
  if (s.stopped) return [];
  const p = projectOf(s.projectId);
  const n = isDb(s) ? 1 : s.replicas;
  return Array.from({ length: n }, (_, i) => ({
    id: hash(`${s.id}-${i}`),
    name: isDb(s) ? `${p.name}-${s.name}-1` : `${p.name}-${s.name}-${i + 1}-${hash(s.currentDeploymentId).slice(0, 6)}`,
    replica: i + 1,
    deployId: s.currentDeploymentId,
    image: s.image,
    state: "running",
    status: "Up 2 days",
    health: s.healthPath || isDb(s) ? "healthy" : undefined,
  }));
}

export function view(s: DemoDb["services"][number]): Service {
  const env = Object.fromEntries(Object.entries(s.env).map(([k, v]) => [k, s.secrets.includes(k) && !/^\s*\{\{.*\}\}\s*$/.test(v) ? MASK : v]));
  return { ...s, env, containers: containers(s) };
}

export const projectNet = (p: Project) => `kipitiny-${p.id}`;
