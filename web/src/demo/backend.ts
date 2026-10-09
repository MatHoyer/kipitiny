import type {
  Account,
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
      if (db.version === 1) return db;
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
  ["GET", /^\/domains$/, () => []],
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
  ["GET", /^\/projects\/([^/]+)$/, ({ m }) => projectOf(m[1])],
  ["GET", /^\/projects\/([^/]+)\/git$/, () => { throw notFound(); }],
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
  ["GET", /^\/services\/([^/]+)\/backups$/, () => []],
  ["GET", /^\/services\/([^/]+)\/schedules$/, () => []],
  ["GET", /^\/services\/([^/]+)\/restores$/, () => []],
  ["GET", /^\/backups$/, () => []],
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
    try {
      return json(200, await handler({ m: match, q: url.searchParams, body }));
    } catch (e) {
      if (e instanceof HttpError) return json(e.status, { error: e.message });
      throw e;
    }
  }
  console.debug(`demo: no fake for ${method} ${path}`);
  return json(501, { error: "Not available in the demo: it runs in your browser, without a server." });
}
