import type {
  Account,
  Backup,
  Cleanup,
  Notifications,
  Schedule,
  AuthState,
  CanvasPoint,
  Connection,
  ConsoleResult,
  Container,
  Deployment,
  Network,
  PgFilter,
  PgRows,
  PgTable,
  Project,
  RedisKeys,
  RedisValue,
  Server,
  Service,
  ServiceStats,
  ServiceUsage,
  Status,
  Topology,
  TopoNode,
  TopoService,
  Uptime,
  Usage,
} from "@/api";
import { seed, service as newService, type DemoDb } from "./seed";
import { demoTemplates } from "./templates";

/*
 * The demo's backend: answers the manager's /api from state kept in this
 * browser. Topology, containers and stats are derived from the services so
 * the UI behaves as on a real server; anything not faked answers 501.
 */

const KEY = "kipitiny-demo";

function load(): DemoDb {
  try {
    const raw = localStorage.getItem(KEY);
    if (raw) {
      const db = JSON.parse(raw) as DemoDb;
      if (db.version === 2) return db;
    }
  } catch {
    // Private mode or a broken value: start over.
  }
  return seed();
}

let db = load();
const save = () => {
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

class HttpError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}
const notFound = () => new HttpError(404, "not found");
const invalid = (m: string) => new HttpError(400, m);

const now = () => new Date().toISOString();
const newId = (prefix: string) => `${prefix}-${Math.random().toString(36).slice(2, 10)}`;
const nameRe = /^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/;
const MASK = "********";

const SERVER: Server = {
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

const projectOf = (id: string) => db.projects.find((p) => p.id === id) ?? (() => { throw notFound(); })();
const serviceOf = (id: string) => db.services.find((s) => s.id === id) ?? (() => { throw notFound(); })();
const isDb = (s: { kind: string }) => s.kind === "postgres" || s.kind === "redis";

/** A short, stable fake container ID. */
const hash = (s: string) => {
  let h = 2166136261;
  for (const c of s) h = Math.imul(h ^ c.charCodeAt(0), 16777619);
  return (h >>> 0).toString(16).padStart(8, "0") + "a1b2";
};

function containers(s: DemoDb["services"][number]): Container[] {
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

function view(s: DemoDb["services"][number]): Service {
  const env = Object.fromEntries(Object.entries(s.env).map(([k, v]) => [k, s.secrets.includes(k) && !/^\s*\{\{.*\}\}\s*$/.test(v) ? MASK : v]));
  return { ...s, env, containers: containers(s) };
}

const projectNet = (p: Project) => `kipitiny-${p.id}`;

function topology(projectId?: string): Topology {
  const projects = projectId ? [projectOf(projectId)] : db.projects;
  let ip = 2;
  const node = (c: Container, endpoints: TopoNode["endpoints"]): TopoNode => ({ ...c, endpoints });
  const used = (s: DemoDb["services"][number], p: Project) =>
    db.services.filter((d) => d.projectId === p.id && isDb(d) && Object.values(s.env).some((v) => v.includes(`db.${d.name}.`))).map((d) => d.id);
  const proxy: TopoNode = node(
    { id: hash("traefik"), name: "kipitiny-traefik", replica: 0, deployId: "", image: "traefik:v3.7", state: "running", status: "Up 9 days" },
    [{ network: "kipitiny-proxy", ip: "172.18.0.2" }],
  );
  return {
    servers: [
      {
        id: "local",
        name: "This server",
        kind: "local",
        traefik: true,
        tunnel: false,
        entrypoints: [
          { name: "web", port: 80, hostPort: "80", redirectTo: "websecure" },
          { name: "websecure", port: 443, hostPort: "443" },
        ],
        proxy,
        manager: { domain: "kipitiny.acme.example", upstream: "kipitiny-manager:3000" },
        networks: [
          { name: "kipitiny-proxy", subnet: "172.18.0.0/16", gateway: "172.18.0.1" },
          ...db.networks.map((n, i) => ({ name: `kipitiny-net-${n.id}`, custom: n.name, customId: n.id, subnet: `172.${30 + i}.0.0/16` })),
          ...projects.map((p, i) => ({ name: projectNet(p), projectId: p.id, subnet: `172.${20 + i}.0.0/16`, gateway: `172.${20 + i}.0.1` })),
        ],
        projects: projects.map((p, pi) => ({
          id: p.id,
          name: p.name,
          network: projectNet(p),
          services: db.services
            .filter((s) => s.projectId === p.id)
            .map(
              (s): TopoService => ({
                id: s.id,
                name: s.name,
                kind: s.kind,
                image: s.image,
                icon: s.icon || undefined,
                domain: s.domain || undefined,
                port: s.port || undefined,
                replicas: s.replicas,
                stopped: s.stopped || undefined,
                volume: isDb(s) ? `kipitiny-${s.id}-data` : undefined,
                uses: used(s, p),
                networks: s.networks,
                hostNetwork: s.hostNetwork || undefined,
                publishedPorts: s.publishedPorts.length ? s.publishedPorts : undefined,
                containers: containers(s).map((c) =>
                  node(c, [
                    { network: projectNet(p), ip: `172.${20 + pi}.0.${ip++}`, aliases: [s.name] },
                    ...(s.domain ? [{ network: "kipitiny-proxy", ip: `172.18.0.${ip++}` }] : []),
                    ...s.networks.map((id) => ({ network: `kipitiny-net-${id}`, ip: `172.30.0.${ip++}`, aliases: [`${p.name}-${s.name}`] })),
                  ]),
                ),
              }),
            ),
        })),
      },
    ],
  };
}

/** A gentle, stable-looking usage curve per service. */
function usage(s: DemoDb["services"][number], at: number): Usage {
  const n = isDb(s) ? 1 : s.replicas;
  const seed = parseInt(hash(s.id).slice(0, 4), 16);
  const wave = Math.sin(at / 60_000 + seed) * 0.5 + 0.5;
  const base = s.kind === "postgres" ? 3 : s.kind === "redis" ? 1 : s.image.startsWith("nginx") ? 0.4 : 6;
  const mem = (s.kind === "postgres" ? 140 : s.kind === "redis" ? 12 : s.image.startsWith("nginx") ? 9 : 85) * 2 ** 20;
  return {
    at: new Date(at).toISOString(),
    replicas: n,
    cpu: Math.round((base + wave * base) * n * 10) / 10,
    memoryBytes: Math.round(mem * n * (0.95 + wave * 0.1)),
    memoryLimitBytes: s.memoryMb ? s.memoryMb * n * 2 ** 20 : undefined,
    netRx: Math.round((s.domain ? 40_000 : 6_000) * (0.5 + wave)),
    netTx: Math.round((s.domain ? 90_000 : 9_000) * (0.5 + wave)),
  };
}

function serviceStats(s: DemoDb["services"][number]): ServiceStats {
  const t = Date.now();
  const history = s.stopped ? [] : Array.from({ length: 30 }, (_, i) => usage(s, t - (29 - i) * 10_000));
  const per = Object.fromEntries(containers(s).map((c) => [c.id.slice(0, 12), { current: history.at(-1) ?? null, history }]));
  return { current: history.at(-1) ?? null, history, containers: per };
}

function rows(table: DemoDb["tables"][number], q: URLSearchParams): PgRows {
  let rs = table.rows;
  const filters = JSON.parse(q.get("filters") || "[]") as PgFilter[];
  for (const f of filters) {
    const i = table.columns.findIndex((c) => c.name === f.column);
    if (i < 0) continue;
    const num = (v: string | null) => (v !== null && v !== "" && !isNaN(Number(v)) ? Number(v) : v);
    rs = rs.filter((r) => {
      const v = r[i];
      switch (f.op) {
        case "null":
          return v === null;
        case "notnull":
          return v !== null;
        case "like":
          return v !== null && new RegExp(`^${(f.value ?? "").replace(/[.*+?^${}()|[\]\\]/g, "\\$&").replace(/%/g, ".*").replace(/_/g, ".")}$`, "i").test(v);
        case "=":
          return v === f.value;
        case "!=":
          return v !== f.value;
        default: {
          const a = num(v), b = num(f.value ?? "");
          if (a === null || b === null) return false;
          return f.op === "<" ? a < b : f.op === "<=" ? a <= b : f.op === ">" ? a > b : a >= b;
        }
      }
    });
  }
  const search = q.get("search")?.toLowerCase();
  if (search) rs = rs.filter((r) => r.some((v) => v?.toLowerCase().includes(search)));
  const order = q.get("order");
  const oi = order ? table.columns.findIndex((c) => c.name === order) : -1;
  if (oi >= 0) {
    const desc = q.get("desc") === "true";
    rs = [...rs].sort((a, b) => {
      const x = a[oi], y = b[oi];
      if (x === y) return 0;
      if (x === null) return 1;
      if (y === null) return -1;
      const c = !isNaN(Number(x)) && !isNaN(Number(y)) ? Number(x) - Number(y) : x.localeCompare(y);
      return desc ? -c : c;
    });
  }
  const limit = Number(q.get("limit")) || 50;
  const offset = Number(q.get("offset")) || 0;
  return {
    columns: table.columns.map((c) => ({ name: c.name, type: c.type, nullable: !!c.nullable, primaryKey: !!c.primaryKey })),
    rows: rs.slice(offset, offset + limit),
    truncated: [],
    hasMore: offset + limit < rs.length,
  };
}

/** The few statements the demo understands; anything else explains itself. */
function pgConsole(query: string): ConsoleResult {
  const q = query.trim().replace(/;$/, "");
  const count = /^select\s+count\(\*\)\s+from\s+(?:public\.)?(\w+)$/i.exec(q);
  if (count) {
    const t = db.tables.find((x) => x.name === count[1]);
    if (!t) return { output: `ERROR:  relation "${count[1]}" does not exist` };
    return { columns: ["count"], rows: [[String(t.rows.length)]] };
  }
  const sel = /^select\s+\*\s+from\s+(?:public\.)?(\w+)(?:\s+limit\s+(\d+))?$/i.exec(q);
  if (sel) {
    const t = db.tables.find((x) => x.name === sel[1]);
    if (!t) return { output: `ERROR:  relation "${sel[1]}" does not exist` };
    const n = Math.min(Number(sel[2] ?? 50), 200);
    return { columns: t.columns.map((c) => c.name), rows: t.rows.slice(0, n), more: t.rows.length > n };
  }
  return { output: "This demo runs no real PostgreSQL. Try: SELECT * FROM users LIMIT 10, or SELECT count(*) FROM invoices." };
}

const glob = (pattern: string) => new RegExp(`^${pattern.replace(/[.+^${}()|[\]\\]/g, "\\$&").replace(/\*/g, ".*").replace(/\?/g, ".")}$`);

function redisConsole(command: string): ConsoleResult {
  const [cmd = "", ...args] = command.trim().split(/\s+/);
  const key = db.redis.find((k) => k.key === args[0]);
  switch (cmd.toUpperCase()) {
    case "PING":
      return { output: "PONG" };
    case "DBSIZE":
      return { output: `(integer) ${db.redis.length}` };
    case "KEYS":
      return { output: db.redis.filter((k) => glob(args[0] ?? "*").test(k.key)).map((k, i) => `${i + 1}) "${k.key}"`).join("\n") || "(empty array)" };
    case "GET":
      return { output: key?.type === "string" ? `"${key.items[0][0]}"` : "(nil)" };
    case "TYPE":
      return { output: key?.type ?? "none" };
    case "TTL":
      return { output: `(integer) ${key ? (key.ttl < 0 ? -1 : Math.round(key.ttl / 1000)) : -2}` };
    case "HGETALL":
      return { output: key?.type === "hash" ? key.items.flat().map((v, i) => `${i + 1}) "${v}"`).join("\n") : "(empty array)" };
  }
  return { output: "This demo runs no real Redis. Try: KEYS *, GET cache:plans, HGETALL session:…, TTL rate:203.0.113.10." };
}

type Ctx = { m: RegExpMatchArray; q: URLSearchParams; body: any }; // eslint-disable-line @typescript-eslint/no-explicit-any
type Route = [method: string, path: RegExp, handler: (c: Ctx) => unknown];

const EVENTS: Notifications["events"] = [
  { type: "deploy.failed", label: "Deployment failed", default: true },
  { type: "deploy.succeeded", label: "Deployment succeeded", default: false },
  { type: "git.sync.failed", label: "Git sync failed", default: true },
  { type: "git.sync.succeeded", label: "Git sync applied changes", default: false },
  { type: "service.restarted", label: "Stopped service restarted", default: true },
  { type: "service.unhealthy", label: "Service unhealthy", default: true },
  { type: "service.healthy", label: "Service healthy again", default: true },
  { type: "uptime.down", label: "Uptime check failing", default: true },
  { type: "uptime.up", label: "Uptime check recovered", default: true },
  { type: "backup.failed", label: "Backup failed", default: true },
  { type: "backup.succeeded", label: "Backup succeeded", default: false },
  { type: "verify.failed", label: "Restore test failed", default: true },
  { type: "verify.succeeded", label: "Restore test passed", default: false },
  { type: "restore.failed", label: "Restore failed", default: true },
  { type: "restore.succeeded", label: "Restore succeeded", default: true },
  { type: "cleanup.failed", label: "Cleanup had errors", default: true },
  { type: "update.available", label: "New kipitiny version", default: true },
];

/** A fresh backup of a service (or the manager), as if it just ran. */
function newBackup(s: DemoDb["services"][number] | null, targetId = "local"): Backup {
  const p = s ? projectOf(s.projectId) : null;
  const kind = !s ? "manager" : s.kind === "postgres" ? "postgres" : "volume";
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
    objectKey: s ? `${p!.name}/${s.name}/${t.slice(0, 19)}${kind === "postgres" ? ".dump" : ".tar.gz"}` : `manager/${t.slice(0, 19)}.db`,
    status: "succeeded",
    sizeBytes: size,
    sha256: Math.random().toString(16).slice(2, 10) + "e3b0c44298fc1c149afbf4c8996fb924",
    pgVersion: kind === "postgres" ? "17.6" : "",
    volumes: kind === "volume" ? [`kipitiny-${s!.id}-data`] : undefined,
    durationMs: kind === "postgres" ? 2100 : 700,
    createdAt: t,
    finishedAt: t,
    verifyDetails: { tables: 0, rows: 0, dbBytes: 0, durationMs: 0 },
  };
}

function cleanupView(): Cleanup {
  return {
    settings: db.cleanup,
    running: false,
    nextRun: db.cleanup.enabled ? new Date(Date.now() + 3 * 86_400_000).toISOString() : undefined,
    lastRun: {
      trigger: "schedule",
      startedAt: new Date(Date.now() - 4 * 86_400_000).toISOString(),
      finishedAt: new Date(Date.now() - 4 * 86_400_000 + 9000).toISOString(),
      servers: [{ server: "This server", containers: 3, images: 7, volumes: 2, networks: 1, buildCache: 0, reclaimed: 1_850_000_000 }],
      deployments: 12,
    },
  };
}

const scheduleFrom = (body: any, base: Partial<Schedule>): Schedule => ({ // eslint-disable-line @typescript-eslint/no-explicit-any
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
function compose(projectId: string, only?: string): string {
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

const routes: Route[] = [
  ["GET", /^\/auth\/state$/, (): AuthState => ({ setupRequired: false, user: { id: "demo", username: "demo", createdAt: now() } })],
  ["GET", /^\/account$/, (): Account => ({ username: "demo", totpEnabled: false, recoveryCodes: 0, passkeys: [] })],
  [
    "GET",
    /^\/status$/,
    (): Status => ({
      version: "demo",
      update: { current: "demo", available: false, canApply: false, reason: "This is a demo running in your browser.", updating: false },
      docker: { version: "27.3.1", apiVersion: "1.47", os: "linux", arch: "amd64" },
    }),
  ],
  ["POST", /^\/auth\/logout$/, () => undefined],

  ["GET", /^\/servers$/, () => [{ ...SERVER, projects: db.projects.length }]],
  ["GET", /^\/domains$/, () => db.domains],
  ["GET", /^\/secret-providers$/, () => []],
  ["GET", /^\/topology$/, () => topology()],
  ["GET", /^\/projects\/([^/]+)\/topology$/, ({ m }) => topology(m[1])],
  ["GET", /^\/canvas$/, () => ({ positions: db.canvas })],
  [
    "PUT",
    /^\/canvas$/,
    ({ body }) => {
      db.canvas = { ...db.canvas, ...(body.positions as Record<string, CanvasPoint>) };
      save();
    },
  ],
  [
    "DELETE",
    /^\/canvas$/,
    () => {
      db.canvas = {};
      save();
    },
  ],

  ["GET", /^\/projects$/, () => db.projects],
  [
    "POST",
    /^\/projects$/,
    ({ body }) => {
      const name = String(body.name ?? "").trim();
      if (!nameRe.test(name)) throw invalid("name must be lowercase letters, digits and dashes (max 40)");
      if (db.projects.some((p) => p.name === name)) throw new HttpError(409, "already exists (name or domain taken)");
      const p: Project = { id: newId("p"), name, serverId: "local", env: {}, secrets: [], createdAt: now(), updatedAt: now() };
      db.projects.push(p);
      save();
      return p;
    },
  ],
  ["GET", /^\/templates$/, () => demoTemplates.map(({ services: _, ...t }) => t)],
  [
    "POST",
    /^\/templates\/([^/]+)\/install$/,
    ({ m, body }) => {
      const t = demoTemplates.find((t) => t.id === m[1]);
      if (!t) throw notFound();
      const values: Record<string, string> = body.values ?? {};
      const missing = t.inputs.filter((i) => i.required && !values[i.name]?.trim());
      if (missing.length) throw invalid(missing.map((i) => `${i.label} is required`).join("; "));
      const name = String(body.newProject?.name ?? "").trim();
      if (!body.projectId && !nameRe.test(name)) throw invalid("name must be lowercase letters, digits and dashes (max 40)");
      const taken = body.projectId ? db.services.filter((s) => s.projectId === projectOf(body.projectId).id).map((s) => s.name) : [];
      const clash = t.services.find((s) => taken.includes(s.name));
      if (clash) throw invalid(`service ${clash.name}: already exists`);
      const names = t.services.map((s) => s.name);
      const plan = { create: names, update: [], unchanged: [], delete: [], orphaned: [], variables: [], warnings: [], deploying: body.dryRun ? [] : names };
      if (body.dryRun) return { projectId: body.projectId, plan };
      let projectId = body.projectId;
      if (!projectId) {
        const p: Project = { id: newId("p"), name, serverId: "local", env: {}, secrets: [], createdAt: now(), updatedAt: now() };
        db.projects.push(p);
        projectId = p.id;
      }
      for (const { domainInput, ...svc } of t.services) {
        db.services.push(
          newService({
            ...svc,
            id: newId("s"),
            kind: "app",
            projectId,
            domain: domainInput ? (values[domainInput] ?? "") : "",
            currentDeploymentId: "",
            createdAt: now(),
            updatedAt: now(),
          }),
        );
      }
      save();
      return { projectId, plan };
    },
  ],
  ["GET", /^\/projects\/([^/]+)$/, ({ m }) => projectOf(m[1])],
  ["GET", /^\/projects\/([^/]+)\/git$/, () => { throw notFound(); }],
  ["GET", /^\/projects\/([^/]+)\/compose$/, ({ m, q }) => compose(m[1], q.get("service") ?? undefined)],
  ["GET", /^\/projects\/([^/]+)\/services$/, ({ m }) => db.services.filter((s) => s.projectId === projectOf(m[1]).id).map(view)],
  [
    "POST",
    /^\/projects\/([^/]+)\/services$/,
    ({ m, body }) => {
      const p = projectOf(m[1]);
      const name = String(body.name ?? "").trim();
      if (!nameRe.test(name)) throw invalid("name must be lowercase letters, digits and dashes (max 40)");
      if (db.services.some((s) => s.projectId === p.id && s.name === name)) throw new HttpError(409, "already exists (name or domain taken)");
      const kind = body.kind ?? "app";
      const image = body.image || (kind === "postgres" ? "postgres:17-alpine" : kind === "redis" ? "redis:8-alpine" : "");
      if (!image) throw invalid("image: repository name must have at least one component");
      const s = newService({
        id: newId("s"),
        name,
        kind,
        image,
        projectId: p.id,
        replicas: kind === "app" ? Number(body.replicas) || 1 : 1,
        port: Number(body.port) || 0,
        domain: body.domain ?? "",
        env: body.env ?? {},
        secrets: body.secrets ?? [],
        memoryMb: Number(body.memoryMb) || 0,
        currentDeploymentId: "",
        createdAt: now(),
        updatedAt: now(),
      });
      db.services.push(s);
      save();
      return view(s);
    },
  ],
  [
    "PUT",
    /^\/projects\/([^/]+)\/env$/,
    ({ m, body }) => {
      const p = projectOf(m[1]);
      p.env = body.env ?? {};
      p.secrets = body.secrets ?? [];
      save();
      return p;
    },
  ],

  ["GET", /^\/services\/([^/]+)$/, ({ m }) => view(serviceOf(m[1]))],
  [
    "PATCH",
    /^\/services\/([^/]+)$/,
    ({ m, body }) => {
      const s = serviceOf(m[1]);
      const patch = { ...body };
      if (patch.env) patch.env = Object.fromEntries(Object.entries(patch.env as Record<string, string>).map(([k, v]) => [k, v === MASK ? (s.env[k] ?? "") : v]));
      delete patch.middlewares;
      Object.assign(s, patch, { updatedAt: now() });
      save();
      return view(s);
    },
  ],
  [
    "PUT",
    /^\/services\/([^/]+)\/name$/,
    ({ m, body }) => {
      const s = serviceOf(m[1]);
      if (!nameRe.test(body.name)) throw invalid("name must be lowercase letters, digits and dashes (max 40)");
      s.name = body.name;
      save();
      return view(s);
    },
  ],
  [
    "PUT",
    /^\/services\/([^/]+)\/networks$/,
    ({ m, body }) => {
      const s = serviceOf(m[1]);
      s.networks = (body.networks as string[]).filter((id) => db.networks.some((n) => n.id === id));
      save();
      return view(s);
    },
  ],
  [
    "DELETE",
    /^\/services\/([^/]+)$/,
    ({ m }) => {
      const s = serviceOf(m[1]);
      db.services = db.services.filter((x) => x.id !== s.id);
      save();
    },
  ],
  [
    "POST",
    /^\/services\/([^/]+)\/deploy$/,
    ({ m }): Deployment => {
      const s = serviceOf(m[1]);
      const d: Deployment = { id: newId("dep"), serviceId: s.id, status: "succeeded", image: s.image, triggeredBy: "user:demo", createdAt: now(), finishedAt: now() };
      db.deployments.unshift(d);
      s.currentDeploymentId = d.id;
      s.stopped = false;
      save();
      return d;
    },
  ],
  [
    "POST",
    /^\/services\/([^/]+)\/(start|stop|restart)$/,
    ({ m }) => {
      const s = serviceOf(m[1]);
      s.stopped = m[2] === "stop";
      save();
      return view(s);
    },
  ],
  ["GET", /^\/services\/([^/]+)\/deployments$/, ({ m }) => db.deployments.filter((d) => d.serviceId === m[1])],
  ["GET", /^\/deployments\/([^/]+)\/log$/, () => "Pulling image…\nStarting replicas…\nHealthy.\nDeployment succeeded (this is a demo: nothing really ran)."],
  ["GET", /^\/services\/([^/]+)\/stats$/, ({ m }) => serviceStats(serviceOf(m[1]))],
  [
    "GET",
    /^\/stats$/,
    () =>
      Object.fromEntries(
        db.services.filter((s) => !s.stopped).map((s): [string, ServiceUsage] => [s.id, { ...usage(s, Date.now()), projectId: s.projectId, serverId: "local" }]),
      ),
  ],
  [
    "GET",
    /^\/services\/([^/]+)\/uptime$/,
    (): Uptime => ({ check: null, recent: [], uptime24h: null, uptime7d: null, uptime30d: null, avgLatencyMs: null }),
  ],
  ["GET", /^\/services\/([^/]+)\/backups$/, ({ m }) => db.backups.filter((b) => b.serviceId === m[1])],
  [
    "POST",
    /^\/services\/([^/]+)\/backups$/,
    ({ m, body }) => {
      const b = newBackup(serviceOf(m[1]), body.targetId);
      db.backups.unshift(b);
      save();
      return b;
    },
  ],
  [
    "POST",
    /^\/projects\/([^/]+)\/backups$/,
    ({ m, body }) => {
      const made = db.services.filter((s) => s.projectId === projectOf(m[1]).id && (isDb(s) || s.volumes.length > 0)).map((s) => newBackup(s, body.targetId));
      db.backups.unshift(...made);
      save();
      return { backups: made };
    },
  ],
  [
    "POST",
    /^\/manager\/backups$/,
    ({ body }) => {
      const b = newBackup(null, body.targetId);
      db.backups.unshift(b);
      save();
      return b;
    },
  ],
  ["GET", /^\/backups$/, ({ q }) => (q.get("projectId") ? db.backups.filter((b) => b.projectId === q.get("projectId")) : db.backups)],
  ["GET", /^\/backups\/([^/]+)$/, ({ m }) => db.backups.find((b) => b.id === m[1]) ?? (() => { throw notFound(); })()],
  [
    "DELETE",
    /^\/backups\/([^/]+)$/,
    ({ m }) => {
      db.backups = db.backups.filter((b) => b.id !== m[1]);
      save();
    },
  ],
  [
    "POST",
    /^\/backups\/([^/]+)\/verify$/,
    ({ m }) => {
      const b = db.backups.find((x) => x.id === m[1]) ?? (() => { throw notFound(); })();
      Object.assign(b, {
        verifyStatus: "succeeded",
        verifiedAt: now(),
        verifyDetails: b.kind === "postgres" ? { tables: db.tables.length, rows: db.tables.reduce((n, t) => n + t.rows.length, 0), dbBytes: b.sizeBytes * 3, durationMs: 4600 } : { tables: 0, rows: 0, dbBytes: 0, files: 3, durationMs: 900 },
      });
      save();
      return b;
    },
  ],
  [
    "POST",
    /^\/backups\/([^/]+)\/restore$/,
    ({ m, body }) => {
      const b = db.backups.find((x) => x.id === m[1]) ?? (() => { throw notFound(); })();
      const svc = serviceOf(body.serviceId || b.serviceId);
      if (body.confirm !== svc.name) throw invalid(`type ${svc.name} to confirm`);
      const r = { id: newId("rs"), backupId: b.id, serviceId: svc.id, status: "succeeded" as const, createdAt: now(), finishedAt: now() };
      db.restores.unshift(r);
      save();
      return r;
    },
  ],
  ["GET", /^\/services\/([^/]+)\/restores$/, ({ m }) => db.restores.filter((r) => r.serviceId === m[1])],
  ["GET", /^\/services\/([^/]+)\/schedules$/, ({ m }) => db.schedules.filter((s) => s.serviceId === m[1])],
  [
    "POST",
    /^\/services\/([^/]+)\/schedules$/,
    ({ m, body }) => {
      const svc = serviceOf(m[1]);
      const s = scheduleFrom(body, { serviceId: svc.id, kind: svc.kind === "postgres" ? "postgres" : "volume" });
      db.schedules.push(s);
      save();
      return s;
    },
  ],
  ["GET", /^\/manager\/schedules$/, () => db.schedules.filter((s) => s.kind === "manager")],
  [
    "POST",
    /^\/manager\/schedules$/,
    ({ body }) => {
      const s = scheduleFrom(body, { kind: "manager" });
      db.schedules.push(s);
      save();
      return s;
    },
  ],
  [
    "PUT",
    /^\/schedules\/([^/]+)$/,
    ({ m, body }) => {
      const i = db.schedules.findIndex((s) => s.id === m[1]);
      if (i < 0) throw notFound();
      db.schedules[i] = scheduleFrom(body, db.schedules[i]);
      save();
      return db.schedules[i];
    },
  ],
  [
    "DELETE",
    /^\/schedules\/([^/]+)$/,
    ({ m }) => {
      db.schedules = db.schedules.filter((s) => s.id !== m[1]);
      save();
    },
  ],

  ["GET", /^\/cloudflare$/, () => ({ connected: false, zones: [], tunnels: [] })],
  ["GET", /^\/git-providers$/, () => []],
  ["GET", /^\/registries$/, () => db.registries],
  [
    "POST",
    /^\/registries$/,
    ({ body }) => {
      const r = { id: newId("reg"), host: String(body.host || "docker.io"), username: String(body.username), password: MASK, createdAt: now() };
      db.registries.push(r);
      save();
      return r;
    },
  ],
  [
    "PUT",
    /^\/registries\/([^/]+)$/,
    ({ m, body }) => {
      const r = db.registries.find((x) => x.id === m[1]) ?? (() => { throw notFound(); })();
      Object.assign(r, { host: body.host, username: body.username });
      save();
      return r;
    },
  ],
  [
    "DELETE",
    /^\/registries\/([^/]+)$/,
    ({ m }) => {
      db.registries = db.registries.filter((r) => r.id !== m[1]);
      save();
    },
  ],
  ["POST", /^\/registries\/([^/]+)\/test$/, () => undefined],
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
      kinds: [{ name: "discord", label: "Discord", fields: [{ key: "webhookUrl", label: "Webhook URL", placeholder: "https://discord.com/api/webhooks/…", required: true, secret: true }] }],
      events: EVENTS,
    }),
  ],
  [
    "POST",
    /^\/notifications\/channels$/,
    ({ body }) => {
      const ch = { id: newId("ch"), name: String(body.name || "Discord"), kind: body.kind || "discord", config: { webhookUrl: MASK }, events: body.events ?? [], enabled: body.enabled ?? true, createdAt: now() };
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
      const t = { id: newId("tok"), name: String(body.name || "token"), scope: body.scope || "read", createdAt: now() };
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
  ["POST", /^\/cleanup\/run$/, () => cleanupView()],
  ["GET", /^\/storage$/, () => [{ id: "local", name: "Local disk", kind: "local", config: {}, createdAt: now() }]],
  [
    "GET",
    /^\/services\/([^/]+)\/connection$/,
    ({ m }): Connection => {
      const s = serviceOf(m[1]);
      return s.kind === "redis"
        ? { host: s.name, port: 6379, user: "", password: "demo-password", url: `redis://:demo-password@${s.name}:6379` }
        : { host: s.name, port: 5432, database: "postgres", user: "app", password: "demo-password", url: `postgres://app:demo-password@${s.name}:5432/postgres` };
    },
  ],

  ["GET", /^\/services\/([^/]+)\/data\/databases$/, () => [{ name: "postgres", bytes: 9_437_184, main: true }]],
  [
    "GET",
    /^\/services\/([^/]+)\/data\/tables$/,
    (): PgTable[] =>
      db.tables.map((t) => ({
        schema: t.schema,
        name: t.name,
        kind: "table",
        rowEstimate: t.rows.length,
        bytes: t.rows.length * 96 + 8192,
        columns: t.columns.map((c) => ({ name: c.name, type: c.type, nullable: !!c.nullable, primaryKey: !!c.primaryKey })),
      })),
  ],
  [
    "GET",
    /^\/services\/([^/]+)\/data\/tables\/([^/]+)\/([^/]+)$/,
    ({ m, q }) => {
      const t = db.tables.find((x) => x.schema === decodeURIComponent(m[2]) && x.name === decodeURIComponent(m[3]));
      if (!t) throw notFound();
      return rows(t, q);
    },
  ],
  [
    "GET",
    /^\/services\/([^/]+)\/data\/keys$/,
    ({ q }): RedisKeys => {
      const re = glob(q.get("pattern") || "*");
      return { keys: db.redis.filter((k) => re.test(k.key)).map((k) => ({ key: k.key, type: k.type, ttl: k.ttl, bytes: JSON.stringify(k.items).length + 48 })), cursor: "0" };
    },
  ],
  [
    "GET",
    /^\/services\/([^/]+)\/data\/key$/,
    ({ q }): RedisValue => {
      const k = db.redis.find((x) => x.key === q.get("key"));
      if (!k) throw notFound();
      // A string's length is its size in bytes, as STRLEN; other types count their items.
      const length = k.type === "string" ? new TextEncoder().encode(k.items[0][0]).length : k.items.length;
      return { type: k.type, length, ttl: k.ttl, items: k.items, truncated: [], cursor: "0" };
    },
  ],
  [
    "POST",
    /^\/services\/([^/]+)\/data\/console$/,
    ({ m, body }) => (serviceOf(m[1]).kind === "redis" ? redisConsole(String(body.query)) : pgConsole(String(body.query))),
  ],

  [
    "GET",
    /^\/networks$/,
    (): Network[] =>
      db.networks.map((n) => ({
        ...n,
        dockerName: `kipitiny-net-${n.id}`,
        services: db.services
          .filter((s) => s.networks.includes(n.id))
          .map((s) => {
            const p = projectOf(s.projectId);
            return { id: s.id, name: s.name, kind: s.kind, projectId: p.id, projectName: p.name, alias: `${p.name}-${s.name}` };
          }),
      })),
  ],
  [
    "POST",
    /^\/networks$/,
    ({ body }) => {
      const name = String(body.name ?? "").trim();
      if (!nameRe.test(name)) throw invalid("name must be lowercase letters, digits and dashes (max 40)");
      if (db.networks.some((n) => n.name === name)) throw invalid(`this server already has a network named ${name}`);
      const n = { id: newId("n"), serverId: "local", name, createdAt: now() };
      db.networks.push(n);
      save();
      return { ...n, dockerName: `kipitiny-net-${n.id}`, services: [] };
    },
  ],
  [
    "DELETE",
    /^\/networks\/([^/]+)$/,
    ({ m }) => {
      db.networks = db.networks.filter((n) => n.id !== m[1]);
      for (const s of db.services) s.networks = s.networks.filter((id) => id !== m[1]);
      save();
    },
  ],
];

/** What the log stream and terminal of a service need to look real. */
export function demoService(id: string) {
  const s = db.services.find((x) => x.id === id);
  if (!s) return null;
  const p = projectOf(s.projectId);
  // Env as the container sees it: references resolved, secrets masked.
  const env = Object.fromEntries(
    Object.entries(s.env).map(([k, v]) => [
      k,
      s.secrets.includes(k) || /PASSWORD|SECRET/.test(k)
        ? "********"
        : v.replace(/\{\{\s*db\.([\w-]+)\.URL\s*\}\}/g, (_, name: string) => {
            const d = db.services.find((x) => x.projectId === s.projectId && x.name === name);
            return d?.kind === "redis" ? `redis://:********@${name}:6379` : `postgres://app:********@${name}:5432/postgres`;
          }).replace(/\{\{\s*project\.(\w+)\s*\}\}/g, (_, key: string) => p.env[key] ?? ""),
    ]),
  );
  const cts = containers(s);
  return { name: s.name, project: p.name, kind: s.kind, image: s.image, env, port: s.port, containers: cts.map((c) => c.name), hostId: cts[0]?.id.slice(0, 12) ?? "" };
}

/** Records a change like the manager's audit log: the route's pattern, and what it targeted. */
const knownIds = () =>
  new Set<string>(
    [db.projects, db.services, db.networks, db.backups, db.schedules, db.domains, db.registries, db.channels, db.tokens, db.deployments].flatMap((list) => list.map((x) => x.id)),
  );

/** Names for IDs, taken before a change (a deleted service still reads by name). */
const names = () => new Map<string, string>([...db.services, ...db.projects].map((x) => [x.id, x.name]));

function audit(method: string, path: string, status: number, ids: Set<string>, named: Map<string, string>, error?: string) {
  if (path.startsWith("/auth/")) return;
  let target = "";
  const pattern = path
    .split("/")
    .map((seg, i) => {
      // New entities aren't listed yet when created: their parent's ID is.
      if (i < 2 || !(ids.has(seg) || /^[a-z]+-[a-z0-9]{6,}$/.test(seg))) return seg;
      target ||= named.get(seg) ?? seg;
      return "{id}";
    })
    .join("/");
  db.audit.unshift({ id: newId("au"), actor: "user:demo", action: `${method} /api${pattern}`, target, status: status === 200 ? (method === "POST" ? 201 : 200) : status, error, createdAt: now() });
  db.audit = db.audit.slice(0, 200);
  save();
}

/** Answers one /api request, as the manager would. */
export async function handle(method: string, url: URL, body: unknown): Promise<Response> {
  const path = url.pathname.replace(/^.*?\/api/, "");
  const json = (status: number, data: unknown) =>
    new Response(data === undefined ? null : typeof data === "string" ? data : JSON.stringify(data), {
      status: data === undefined && status === 200 ? 204 : status,
      headers: { "Content-Type": typeof data === "string" ? "text/plain" : "application/json" },
    });
  for (const [m, re, handler] of routes) {
    if (m !== method) continue;
    const match = path.match(re);
    if (!match) continue;
    const ids = knownIds();
    const named = names();
    try {
      const out = await handler({ m: match, q: url.searchParams, body });
      if (method !== "GET") audit(method, path, 200, ids, named);
      return json(200, out);
    } catch (e) {
      if (e instanceof HttpError) {
        if (method !== "GET") audit(method, path, e.status, ids, named, e.message);
        return json(e.status, { error: e.message });
      }
      throw e;
    }
  }
  console.debug(`demo: no fake for ${method} ${path}`);
  return json(501, { error: "Not available in the demo: it runs in your browser, without a server." });
}
