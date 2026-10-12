import type {
  Account,
  Backup,
  Restore,
  Cleanup,
  Notifications,
  Schedule,
  AuthState,
  SecretItem,
  SecretProvider,
  CanvasPoint,
  Connection,
  ConsoleResult,
  Container,
  Deployment,
  MongoDocs,
  Network,
  PgFilter,
  PgRows,
  RowChanges,
  DocChanges,
  KeyChanges,
  NewColumn,
  ChangeResult,
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
  UptimeCheck,
  UptimeInput,
  UptimeResult,
  Usage,
} from "@/api";
import { files } from "./files";
import { seed, service as newService, type DemoDb } from "./seed";
import { demoLogos, demoTemplates } from "./templates";

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
      if (db.version === 3) return db;
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
const isDb = (s: { kind: string }) => s.kind !== "app";

/** How the demo's databases are reached, per kind. */
const dbDemo: Record<string, { image: string; port: number; url: (name: string, password: string) => string; user: string; database?: string }> = {
  postgres: { image: "postgres:17-alpine", port: 5432, user: "app", database: "postgres", url: (n, pw) => `postgres://app:${pw}@${n}:5432/postgres` },
  redis: { image: "redis:8-alpine", port: 6379, user: "", url: (n, pw) => `redis://:${pw}@${n}:6379` },
  mysql: { image: "mysql:8.4", port: 3306, user: "app", database: "app", url: (n, pw) => `mysql://app:${pw}@${n}:3306/app` },
  mariadb: { image: "mariadb:11.8", port: 3306, user: "app", database: "app", url: (n, pw) => `mysql://app:${pw}@${n}:3306/app` },
  mongodb: { image: "mongo:8.2", port: 27017, user: "app", database: "app", url: (n, pw) => `mongodb://app:${pw}@${n}:27017/app?authSource=admin` },
};

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
  // Saved before maintenance pages existed.
  return { ...s, maintenance: s.maintenance ?? { enabled: false }, env, containers: containers(s) };
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
                routePending: s.routePending || undefined,
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

/** Saves key edits like the manager does: every check first, then the writes, as one script would. */
function keyChanges(key: string, ch: KeyChanges, apply: boolean): ChangeResult {
  if (ch.create) {
    if (db.redis.some((x) => x.key === key)) throw invalid(`key ${JSON.stringify(key)} already exists`);
    const ins = ch.inserts ?? [];
    const q = (a: string) => (a && !/[\s"'\\]/.test(a) ? a : JSON.stringify(a));
    const verb = { string: "SET", hash: "HSET", list: "RPUSH", set: "SADD", zset: "ZADD" }[ch.type as "string"];
    const statements = ins.map((it) => [verb, key, ...(ch.type === "list" ? [it[1]] : ch.type === "zset" ? [it[1], it[0]] : it)].map(q).join(" "));
    if (apply) {
      const items = ch.type === "list" ? ins.map((it, i) => [String(i), it[1]]) : ins;
      db.redis.push({ key, type: ch.type as DemoDb["redis"][number]["type"], ttl: -1, items });
      save();
    }
    return { statements, applied: apply };
  }
  const k = db.redis.find((x) => x.key === key);
  if (!k || k.type !== ch.type) throw invalid("the key changed or was deleted since it was loaded; reload and make the change again");
  const arg = (a: string) => (a && !/[\s"'\\]/.test(a) ? a : JSON.stringify(a));
  const cmd = (...a: string[]) => a.map(arg).join(" ");
  const statements: string[] = [];
  let items = k.items.map((it) => [...it]);
  const find = (first: string) => {
    const it = items.find((x) => x[0] === first);
    if (!it) throw invalid(`${first} changed or was deleted since it was loaded; reload and make the change again`);
    return it;
  };
  if (ch.delete) statements.push(cmd("DEL", key));
  else if (k.type === "string") {
    const e = ch.edits![0];
    statements.push(cmd("SET", key, e.set[0], "KEEPTTL"));
    items = [[e.set[0]]];
  } else {
    for (const e of ch.edits ?? []) {
      const it = find(e.item[0]);
      if (k.type === "hash") statements.push(cmd("HSET", key, e.set[0], e.set[1]));
      if (k.type === "list") statements.push(cmd("LSET", key, e.item[0], e.set[1]));
      if (k.type === "set") statements.push(cmd("SREM", key, e.item[0]), cmd("SADD", key, e.set[0]));
      if (k.type === "zset") statements.push(...(e.set[0] === e.item[0] ? [cmd("ZADD", key, "XX", e.set[1], e.set[0])] : [cmd("ZREM", key, e.item[0]), cmd("ZADD", key, e.set[1], e.set[0])]));
      it.splice(0, it.length, ...e.set);
    }
    const gone = new Set((ch.deletes ?? []).map((d) => d[0]));
    for (const d of ch.deletes ?? []) {
      find(d[0]);
      statements.push(k.type === "hash" ? cmd("HDEL", key, d[0]) : k.type === "set" ? cmd("SREM", key, d[0]) : k.type === "zset" ? cmd("ZREM", key, d[0]) : cmd("LSET", key, d[0], "__kipitiny_deleted"));
    }
    if (k.type === "list" && gone.size) statements.push(cmd("LREM", key, "0", "__kipitiny_deleted"));
    items = items.filter((it) => !gone.has(it[0]));
    for (const ins of ch.inserts ?? []) {
      statements.push(k.type === "hash" ? cmd("HSET", key, ins[0], ins[1]) : k.type === "list" ? cmd("RPUSH", key, ins[1]) : k.type === "set" ? cmd("SADD", key, ins[0]) : cmd("ZADD", key, ins[1], ins[0]));
      items.push(k.type === "list" ? ["", ins[1]] : [...ins]);
    }
    if (k.type === "list") items = items.map((it, i) => [String(i), it[1]]);
  }
  if (!statements.length) throw invalid("no changes to save");
  if (apply) {
    if (ch.delete) db.redis = db.redis.filter((x) => x !== k);
    else k.items = items;
    save();
  }
  return { statements, applied: apply };
}

/** The demo's documents are its tables' rows; _id is the row's position. */
const demoOid = (i: number) => ({ $oid: `6aca9c0c13d4e72f99${String(i).padStart(6, "0")}` });

/** Saves document edits like the manager does, onto the demo table behind the collection. */
function docChanges(t: DemoDb["tables"][number], ch: DocChanges, apply: boolean): ChangeResult {
  const coll = /^[A-Za-z_][A-Za-z0-9_]*$/.test(t.name) ? `db.${t.name}` : `db.getCollection(${JSON.stringify(t.name)})`;
  const obj = (fields: [string, string][]) => `{${fields.map(([k, v]) => `${JSON.stringify(k)}: ${v}`).join(", ")}}`;
  const indexOf = (id: string) => t.rows.findIndex((_, i) => JSON.stringify(demoOid(i)) === JSON.stringify(JSON.parse(id)));
  const statements: string[] = [];
  const rows = t.rows.map((r) => [...r]);
  const gone = new Set<number>();
  for (const d of ch.deletes ?? []) {
    statements.push(`${coll}.deleteOne(${obj([["_id", d.id]])})`);
    gone.add(indexOf(d.id));
  }
  for (const u of ch.updates ?? []) {
    const names = Object.keys(u.set).sort();
    const filter = obj([["_id", u.id], ...[...names, ...u.unset].map((k): [string, string] => [k, u.was[k] ?? '{"$exists": false}'])]);
    const update = obj([...(names.length ? [["$set", obj(names.map((k) => [k, u.set[k]]))] as [string, string]] : []), ...(u.unset.length ? [["$unset", obj(u.unset.map((k) => [k, '""']))] as [string, string]] : [])]);
    statements.push(`${coll}.updateOne(${filter}, ${update})`);
    const r = rows[indexOf(u.id)];
    if (!r) throw invalid(`document ${u.id} changed or was deleted since it was loaded; reload and make the change again`);
    for (const [k, v] of Object.entries(u.set)) {
      const j = t.columns.findIndex((c) => c.name === k);
      if (j >= 0) r[j] = JSON.parse(v) === null ? null : String(JSON.parse(v));
    }
    for (const k of u.unset) {
      const j = t.columns.findIndex((c) => c.name === k);
      if (j >= 0) r[j] = null;
    }
  }
  for (const doc of ch.inserts ?? []) {
    statements.push(`${coll}.insertOne(${doc})`);
    const d = JSON.parse(doc) as Record<string, unknown>;
    rows.push(t.columns.map((c) => (d[c.name] === undefined || d[c.name] === null ? null : String(d[c.name]))));
  }
  if (!statements.length) throw invalid("no changes to save");
  if (apply) {
    t.rows = rows.filter((_, i) => !gone.has(i));
    save();
  }
  return { statements, applied: apply };
}

/** Saves edits from the browser like the manager does: checked rows, statements as it writes them. */
function tableChanges(t: DemoDb["tables"][number], ch: RowChanges, apply: boolean, postgres: boolean): ChangeResult {
  const q = (s: string) => (postgres ? `"${s.replace(/"/g, '""')}"` : `\`${s.replace(/`/g, "``")}\``);
  const lit = (v: string | null) => (v === null ? "NULL" : `'${v.replace(/'/g, "''")}'`);
  const name = postgres ? `${q(t.schema)}.${q(t.name)}` : q(t.name);
  const col = (c: string) => {
    const i = t.columns.findIndex((x) => x.name === c);
    if (i < 0) throw invalid(`unknown column "${c}"`);
    return i;
  };
  const text = (c: string, v: string | null) => (v === null ? `${q(c)} IS NULL` : postgres ? `${q(c)}::text = ${lit(v)}` : `CAST(${q(c)} AS CHAR) COLLATE utf8mb4_bin = ${lit(v)}`);
  const where = (key: Record<string, string>) => Object.entries(key).map(([c, v]) => `${q(c)} = ${lit(v)}`);
  const find = (key: Record<string, string>, label: string, was: Record<string, string | null> = {}) => {
    const r = rows.find((r) => Object.entries(key).every(([c, v]) => r[col(c)] === v) && Object.entries(was).every(([c, v]) => r[col(c)] === v));
    if (!r) throw invalid(`${label} changed or was deleted since it was loaded; reload and make the change again`);
    return r;
  };
  const label = (key: Record<string, string>) => "row " + Object.entries(key).map(([c, v]) => `${c} ${v}`).join(", ");
  // Changed on a copy, kept only once every statement went through: one transaction.
  const rows = t.rows.map((r) => [...r]);
  const statements: string[] = [];
  for (const d of ch.deletes ?? []) {
    statements.push(`DELETE FROM ${name} WHERE ${where(d.key).join(" AND ")};`);
    rows.splice(rows.indexOf(find(d.key, label(d.key))), 1);
  }
  for (const u of ch.updates ?? []) {
    const set = Object.entries(u.set).map(([c, v]) => `${q(c)} = ${lit(v)}`);
    const conds = [...where(u.key), ...Object.entries(u.was).map(([c, v]) => text(c, v))];
    statements.push(`UPDATE ${name} SET ${set.join(", ")} WHERE ${conds.join(" AND ")};`);
    const r = find(u.key, label(u.key), u.was);
    for (const [c, v] of Object.entries(u.set)) r[col(c)] = v;
  }
  for (const ins of ch.inserts ?? []) {
    const cs = Object.keys(ins);
    statements.push(cs.length ? `INSERT INTO ${name} (${cs.map(q).join(", ")}) VALUES (${cs.map((c) => lit(ins[c])).join(", ")});` : postgres ? `INSERT INTO ${name} DEFAULT VALUES;` : `INSERT INTO ${name} () VALUES ();`);
    const pk = t.columns.findIndex((c) => c.primaryKey);
    rows.push(t.columns.map((c, i) => (c.name in ins ? ins[c.name] : i === pk ? String(Math.max(0, ...rows.map((r) => Number(r[pk]) || 0)) + 1) : null)));
  }
  if (!statements.length) throw invalid("no changes to save");
  if (apply) {
    t.rows = rows;
    save();
  }
  return { statements, applied: apply };
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

const backupKindOf = (kind: string) => (kind === "postgres" ? "postgres" : kind === "redis" || kind === "app" ? "volume" : "dump");

/** A fresh backup of a service (or the manager), as if it just ran. */
/** The demo's password manager: Proton Pass, whose CLI the real manager fetches on first use. */
const passwordManager = (): SecretProvider => ({
  id: "protonpass",
  name: "Proton Pass",
  scheme: "pass",
  tokenLabel: "Personal access token",
  example: "pass://Vault/Item/password",
  help: "This is a demo: any token connects, to sample vaults. On a server, create one with `pass-cli personal-access-token create --name kipitiny --expiration 1y`.",
  available: true,
  connected: db.passwordManagers.includes("protonpass"),
});

/** The demo's Proton Pass helper: pulled once per page load, running for 5 minutes after each use. */
const helper = { pulled: false, until: 0 };

/** What the manager would do first: pull the CLI if missing, start its helper if idle. */
const helperSteps = () => {
  const steps = [...(helper.pulled ? [] : ["image"]), ...(Date.now() < helper.until ? [] : ["helper"])];
  helper.pulled = true;
  helper.until = Date.now() + 5 * 60_000;
  return steps;
};

/** A ?progress answer: each step after a beat of work, as NDJSON, then the result (an Error ends it failed). */
const progressed = (steps: string[], result: unknown) =>
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
const runs = new Map<string, { start: number; steps: [string, number][] }>();

/** Where a run is: its current step and how far into it (0–1), or null once it's over (and forgotten). */
function runStep(id: string): { step: string; progress: number } | null {
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
function startBackup(b: Backup): Backup {
  const upload = b.kind === "postgres" ? 3200 : 2200;
  const steps: [string, number][] =
    b.kind === "manager" ? [["snapshot", 900], ["upload", upload]] : b.kind === "volume" ? [["helper", 1100], ["upload", upload]] : [["prepare", 900], ["upload", upload]];
  runs.set(b.id, { start: Date.now(), steps });
  return Object.assign(b, { status: "running" as const, finishedAt: undefined });
}

/** A backup as it stands: its step and bytes while running, done once its steps are. */
function backingUp(b: Backup): Backup {
  if (b.status !== "running") return b;
  const at = runStep(b.id);
  if (at) return { ...b, step: at.step as Backup["step"], stepBytes: at.step === "upload" ? Math.round(b.sizeBytes * at.progress) : undefined };
  Object.assign(b, { status: "succeeded", finishedAt: now() });
  save();
  return b;
}

/** A restore as it stands, like backingUp. */
function restoring(r: Restore): Restore {
  if (r.status !== "running") return r;
  const at = runStep(r.id);
  if (at) return { ...r, step: at.step as Restore["step"] };
  Object.assign(r, { status: "succeeded", finishedAt: now() });
  save();
  return r;
}

/** Deployments an install started, as the manager runs them: each starts at `start` and ends at `end` (ms). */
const scheduled: { serviceId: string; start: number; end: number; id?: string }[] = [];

/** Starts the scheduled deployments whose time came and finishes those whose time is up. */
function tickDeploys() {
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
const verifyStarts = new Map<string, number>();

/** A backup as its restore test stands: walks through the steps a real one takes, then passes. */
function verifying(b: Backup): Backup {
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

const connectedManager = (id: string) => {
  if (!db.passwordManagers.includes(id)) throw invalid("this password manager isn't connected");
};

/** The sample vaults: item titles and the fields a reference can name. */
const vaults: Record<string, [string, string[]][]> = {
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
const rand = (n: number) => {
  const x = Math.sin(n * 12.9898) * 43758.5453;
  return x - Math.floor(x);
};

function uptime(s: DemoDb["services"][number]): Uptime {
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

function newBackup(s: DemoDb["services"][number] | null, targetId = "local"): Backup {
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

/** The demo's cleanup run by hand: when it started, while it walks through its steps. */
let cleanupStart: number | null = null;
let cleanupLast: Cleanup["lastRun"] = {
  trigger: "schedule",
  startedAt: new Date(Date.now() - 4 * 86_400_000).toISOString(),
  finishedAt: new Date(Date.now() - 4 * 86_400_000 + 9000).toISOString(),
  servers: [{ server: "This server", containers: 3, images: 7, volumes: 2, networks: 1, buildCache: 0, reclaimed: 1_850_000_000 }],
  deployments: 12,
};

function cleanupView(): Cleanup {
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

/** Runs a volume files fake, its errors as HTTP ones. */
function viaFiles<T>(f: () => T): T {
  try {
    return f();
  } catch (e) {
    if (e instanceof files.FilesError) throw new HttpError(e.status, e.message);
    throw e;
  }
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
  [
    "POST",
    /^\/servers$/,
    ({ q }) => {
      const refused = new Error("this is a demo: it can't reach other servers (ssh: connect to host: connection refused)");
      if (!q.has("progress")) throw invalid(refused.message);
      return progressed(["connect"], refused);
    },
  ],
  [
    "GET",
    /^\/update\/preflight$/,
    () => {
      cleanupView(); // moves a running cleanup along
      const busy = cleanupStart !== null;
      return {
        ready: !busy,
        checks: [
          { id: "version", state: "ok", detail: "demo → 0.27.0" },
          busy
            ? { id: "tasks", state: "block", detail: "in progress: the cleanup. The restart would cut it short" }
            : { id: "tasks", state: "ok", detail: "no deploy, backup, restore or cleanup running" },
          { id: "docker", state: "ok", detail: "Docker 27.3.1 answers" },
          { id: "backup", state: "warn", detail: "the last manager backup is 4 days old: back it up first to have a way back" },
        ],
      };
    },
  ],
  ["GET", /^\/ssh-key$/, () => ({ publicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDemoDemoDemoDemoDemoDemoDemoDemoDemoDemo kipitiny" })],
  ["GET", /^\/domains$/, () => db.domains],
  ["GET", /^\/secret-providers$/, () => [passwordManager()]],
  [
    "PUT",
    /^\/secret-providers\/([^/]+)$/,
    ({ m, q, body }) => {
      if (m[1] !== "protonpass") throw notFound();
      if (!String(body.token ?? "").trim()) throw invalid("a token is required");
      db.passwordManagers = ["protonpass"];
      save();
      return q.has("progress") ? progressed([...helperSteps(), "login", "verify"], passwordManager()) : passwordManager();
    },
  ],
  [
    "DELETE",
    /^\/secret-providers\/([^/]+)$/,
    ({ m }) => {
      db.passwordManagers = db.passwordManagers.filter((id) => id !== m[1]);
      save();
    },
  ],
  [
    "POST",
    /^\/secret-providers\/([^/]+)\/test$/,
    ({ m, q }) => (connectedManager(m[1]), q.has("progress") ? progressed([...helperSteps(), "session"], undefined) : undefined),
  ],
  ["GET", /^\/secret-providers\/([^/]+)\/vaults$/, ({ m }) => (connectedManager(m[1]), Object.keys(vaults))],
  [
    "GET",
    /^\/secret-providers\/([^/]+)\/items$/,
    ({ m, q }): SecretItem[] => {
      connectedManager(m[1]);
      const vault = q.get("vault") ?? "";
      return (vaults[vault] ?? []).map(([title, fields]) => ({ title, fields: fields.map((name) => ({ name, ref: `pass://${vault}/${title}/${name}` })) }));
    },
  ],
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
  ["GET", /^\/templates\/logos$/, () => demoLogos],
  [
    "POST",
    /^\/templates\/([^/]+)\/install$/,
    ({ m, body }) => {
      const t = demoTemplates.find((t) => t.id === m[1]);
      if (!t) throw notFound();
      const values: Record<string, string> = body.values ?? {};
      const missing = t.inputs.filter((i) => i.required && (i.type === "checkbox" ? values[i.name] !== "true" : !values[i.name]?.trim()));
      if (missing.length) throw invalid(missing.map((i) => (i.type === "checkbox" ? `${i.label} must be checked` : `${i.label} is required`)).join("; "));
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
      // Databases deploy first, then the apps once they're up, as the manager does.
      const dbsFor = t.services.some(isDb) ? 2600 : 0;
      for (const { domainInput, ...svc } of t.services) {
        const s = newService({
          ...svc,
          id: newId("s"),
          projectId,
          domain: domainInput ? (values[domainInput] ?? "") : "",
          currentDeploymentId: "",
          createdAt: now(),
          updatedAt: now(),
        });
        db.services.push(s);
        const start = Date.now() + (isDb(s) ? 0 : dbsFor);
        scheduled.push({ serviceId: s.id, start, end: start + (isDb(s) ? 2400 : 3000) });
      }
      tickDeploys();
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
      const image = body.image || (dbDemo[kind]?.image ?? "");
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
      const routed = `${s.domain}:${s.port}`;
      Object.assign(s, patch, { updatedAt: now() });
      if (`${s.domain}:${s.port}` !== routed && s.currentDeploymentId && !s.stopped) s.routePending = true;
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
    "PUT",
    /^\/services\/([^/]+)\/maintenance$/,
    ({ m, body }) => {
      const s = serviceOf(m[1]);
      if (body.enabled && !(s.domain && s.port)) throw invalid("maintenance mode needs a domain and a container port");
      s.maintenance = body;
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
      s.routePending = false;
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
  ["GET", /^\/services\/([^/]+)\/files$/, ({ m, q }) => viaFiles(() => files.list(serviceOf(m[1]), q.get("path") ?? ""))],
  ["GET", /^\/services\/([^/]+)\/files\/content$/, ({ m, q }) => viaFiles(() => files.read(serviceOf(m[1]), q.get("path") ?? ""))],
  ["PUT", /^\/services\/([^/]+)\/files\/content$/, ({ m, q, body }) => viaFiles(() => files.write(serviceOf(m[1]), q.get("path") ?? "", body, q))],
  ["POST", /^\/services\/([^/]+)\/files\/mkdir$/, ({ m, body }) => viaFiles(() => files.mkdir(serviceOf(m[1]), body.path))],
  ["POST", /^\/services\/([^/]+)\/files\/move$/, ({ m, body }) => viaFiles(() => files.transfer(serviceOf(m[1]), body.from, body.to, false))],
  ["POST", /^\/services\/([^/]+)\/files\/copy$/, ({ m, body }) => viaFiles(() => files.transfer(serviceOf(m[1]), body.from, body.to, true))],
  ["POST", /^\/services\/([^/]+)\/files\/delete$/, ({ m, body }) => viaFiles(() => files.remove(serviceOf(m[1]), body.paths ?? []))],
  ["GET", /^\/services\/([^/]+)\/deployments$/, ({ m }) => (tickDeploys(), db.deployments.filter((d) => d.serviceId === m[1]))],
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
  ["GET", /^\/services\/([^/]+)\/uptime$/, ({ m }) => uptime(serviceOf(m[1]))],
  [
    "PUT",
    /^\/services\/([^/]+)\/uptime$/,
    ({ m, body }) => {
      const s = serviceOf(m[1]);
      const input = body as UptimeInput;
      const prev = db.uptime.find((c) => c.serviceId === s.id);
      const now = new Date().toISOString();
      const chk: UptimeCheck = { ...input, serviceId: s.id, down: false, changedAt: prev?.changedAt ?? now, createdAt: prev?.createdAt ?? now };
      db.uptime = [...db.uptime.filter((c) => c.serviceId !== s.id), chk];
      save();
      return uptime(s);
    },
  ],
  [
    "DELETE",
    /^\/services\/([^/]+)\/uptime$/,
    ({ m }) => {
      db.uptime = db.uptime.filter((c) => c.serviceId !== m[1]);
      save();
    },
  ],
  ["GET", /^\/services\/([^/]+)\/backups$/, ({ m }) => db.backups.filter((b) => b.serviceId === m[1]).map(backingUp)],
  [
    "POST",
    /^\/services\/([^/]+)\/backups$/,
    ({ m, body }) => {
      const b = startBackup(newBackup(serviceOf(m[1]), body.targetId));
      db.backups.unshift(b);
      save();
      return backingUp(b);
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
      const b = startBackup(newBackup(null, body.targetId));
      db.backups.unshift(b);
      save();
      return backingUp(b);
    },
  ],
  ["GET", /^\/backups$/, ({ q }) => (q.get("projectId") ? db.backups.filter((b) => b.projectId === q.get("projectId")) : db.backups).map(backingUp)],
  ["GET", /^\/backups\/([^/]+)$/, ({ m }) => verifying(backingUp(db.backups.find((b) => b.id === m[1]) ?? (() => { throw notFound(); })()))],
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
      verifyStarts.set(b.id, Date.now());
      Object.assign(b, { verifyStatus: "running", verifyError: undefined });
      save();
      return verifying(b);
    },
  ],
  [
    "POST",
    /^\/backups\/([^/]+)\/restore$/,
    ({ m, body }) => {
      const b = db.backups.find((x) => x.id === m[1]) ?? (() => { throw notFound(); })();
      const svc = serviceOf(body.serviceId || b.serviceId);
      if (body.confirm !== svc.name) throw invalid(`type ${svc.name} to confirm`);
      const r: Restore = { id: newId("rs"), backupId: b.id, serviceId: svc.id, status: "running", createdAt: now() };
      const steps: [string, number][] =
        b.kind === "volume"
          ? [["extract", 1500], ["stopservice", 700], ["replace", 1200], ["start", 800]]
          : b.kind === "dump"
            ? [["stop", 700], ["check", 1000], ["scratch", 1500], ["load", 1500], ["start", 700]]
            : [["stop", 700], ["load", 2200], ["swap", 900], ["start", 700]];
      runs.set(r.id, { start: Date.now(), steps });
      db.restores.unshift(r);
      save();
      return restoring(r);
    },
  ],
  ["GET", /^\/services\/([^/]+)\/restores$/, ({ m }) => db.restores.filter((r) => r.serviceId === m[1]).map(restoring)],
  ["GET", /^\/services\/([^/]+)\/schedules$/, ({ m }) => db.schedules.filter((s) => s.serviceId === m[1])],
  [
    "POST",
    /^\/services\/([^/]+)\/schedules$/,
    ({ m, body }) => {
      const svc = serviceOf(m[1]);
      const s = scheduleFrom(body, { serviceId: svc.id, kind: backupKindOf(svc.kind) });
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
  ["POST", /^\/registries\/([^/]+)\/test$/, ({ q }) => (q.has("progress") ? progressed(["login"], undefined) : undefined)],
  ["POST", /^\/git-providers\/([^/]+)\/test$/, ({ q }) => (q.has("progress") ? progressed(["token", "repos"], undefined) : undefined)],
  ["POST", /^\/cloudflare\/test$/, ({ q }) => (q.has("progress") ? progressed(["zones"], undefined) : undefined)],
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
    ({ m }) => [serviceOf(m[1]).kind === "postgres" ? { name: "postgres", bytes: 9_437_184, main: true } : { name: "app", bytes: 9_437_184, main: true }],
  ],
  [
    "GET",
    /^\/services\/([^/]+)\/data\/tables$/,
    ({ m }): PgTable[] => {
      // The same demo data in every kind: MySQL's schema is its database, MongoDB's tables are collections.
      const kind = serviceOf(m[1]).kind;
      return db.tables.map((t) => ({
        schema: kind === "postgres" ? t.schema : "app",
        name: t.name,
        kind: kind === "mongodb" ? "collection" : "table",
        rowEstimate: t.rows.length,
        bytes: t.rows.length * 96 + 8192,
        columns:
          kind === "mongodb" ? [] : t.columns.map((c) => ({ name: c.name, type: c.type, nullable: !!c.nullable, primaryKey: !!c.primaryKey })),
      }));
    },
  ],
  [
    "GET",
    /^\/services\/([^/]+)\/data\/tables\/([^/]+)\/([^/]+)$/,
    ({ m, q }) => {
      const postgres = serviceOf(m[1]).kind === "postgres";
      const t = db.tables.find((x) => (!postgres || x.schema === decodeURIComponent(m[2])) && x.name === decodeURIComponent(m[3]));
      if (!t) throw notFound();
      return rows(t, q);
    },
  ],
  [
    "POST",
    /^\/services\/([^/]+)\/data\/tables\/([^/]+)\/([^/]+)\/changes$/,
    ({ m, body }): ChangeResult => {
      const postgres = serviceOf(m[1]).kind === "postgres";
      const t = db.tables.find((x) => (!postgres || x.schema === decodeURIComponent(m[2])) && x.name === decodeURIComponent(m[3]));
      if (!t) throw notFound();
      return tableChanges(t, body.changes as RowChanges, !!body.apply, postgres);
    },
  ],
  [
    "GET",
    /^\/services\/([^/]+)\/data\/collections\/([^/]+)\/documents$/,
    ({ m, q }): MongoDocs => {
      const t = db.tables.find((x) => x.name === decodeURIComponent(m[2]));
      if (!t) throw notFound();
      // A filter on one field's value is all the demo understands.
      let filter: Record<string, unknown> = {};
      try {
        filter = JSON.parse(q.get("filter") || "{}");
      } catch {
        throw invalid("the filter is a JSON object, like {\"field\": 1}");
      }
      const docs = t.rows
        .map((r, i) => ({ _id: demoOid(i), ...Object.fromEntries(t.columns.map((c, j) => [c.name, r[j]])) }))
        .filter((d) => Object.entries(filter).every(([k, v]) => String((d as Record<string, unknown>)[k]) === String(v)));
      const skip = Number(q.get("skip")) || 0;
      const limit = Number(q.get("limit")) || 25;
      return { documents: docs.slice(skip, skip + limit).map((d) => JSON.stringify(d)), truncated: [], hasMore: docs.length > skip + limit };
    },
  ],
  [
    "POST",
    /^\/services\/([^/]+)\/data\/tables$/,
    ({ m, body }): ChangeResult => {
      const postgres = serviceOf(m[1]).kind === "postgres";
      const name = String(body.name ?? "").trim();
      const schema = postgres ? String(body.schema || "public") : "app";
      const columns = (body.columns ?? []) as NewColumn[];
      if (!name || !columns.length) throw invalid("a table needs a name and at least one column");
      if (db.tables.some((t) => t.name === name && (!postgres || t.schema === schema))) throw invalid(`relation "${name}" already exists`);
      const q = (x: string) => (postgres ? `"${x}"` : `\`${x}\``);
      const pk = columns.filter((c) => c.primaryKey).map((c) => q(c.name));
      const lines = columns.map((c) => `  ${q(c.name)} ${c.type}${!c.nullable && !c.primaryKey ? " NOT NULL" : ""}${c.default ? ` DEFAULT ${c.default}` : ""}`);
      if (pk.length) lines.push(`  PRIMARY KEY (${pk.join(", ")})`);
      const statement = `CREATE TABLE ${postgres ? `${q(schema)}.${q(name)}` : q(name)} (\n${lines.join(",\n")}\n);`;
      if (body.apply) {
        db.tables.push({ schema, name, columns: columns.map((c) => ({ name: c.name, type: c.type, primaryKey: c.primaryKey, nullable: c.nullable })), rows: [] });
        save();
      }
      return { statements: [statement], applied: !!body.apply };
    },
  ],
  [
    "POST",
    /^\/services\/([^/]+)\/data\/tables\/([^/]+)\/([^/]+)\/drop$/,
    ({ m, body }): ChangeResult => {
      const postgres = serviceOf(m[1]).kind === "postgres";
      const t = db.tables.find((x) => (!postgres || x.schema === decodeURIComponent(m[2])) && x.name === decodeURIComponent(m[3]));
      if (!t) throw notFound();
      if (body.apply) {
        db.tables = db.tables.filter((x) => x !== t);
        save();
      }
      return { statements: [postgres ? `DROP TABLE "${t.schema}"."${t.name}";` : `DROP TABLE \`${t.name}\`;`], applied: !!body.apply };
    },
  ],
  [
    "POST",
    /^\/services\/([^/]+)\/data\/collections$/,
    ({ body }): ChangeResult => {
      const name = String(body.name ?? "").trim();
      if (!/^[A-Za-z_][A-Za-z0-9_.-]{0,119}$/.test(name)) throw invalid("a collection name has letters, digits, _ . and -, starting with a letter or _");
      if (db.tables.some((t) => t.name === name)) throw invalid(`app already has a collection ${name}`);
      if (body.apply) {
        db.tables.push({ schema: "public", name, columns: [], rows: [] });
        save();
      }
      return { statements: [`db.createCollection(${JSON.stringify(name)})`], applied: !!body.apply };
    },
  ],
  [
    "POST",
    /^\/services\/([^/]+)\/data\/collections\/([^/]+)\/drop$/,
    ({ m, body }): ChangeResult => {
      const t = db.tables.find((x) => x.name === decodeURIComponent(m[2]));
      if (!t) throw notFound();
      if (body.apply) {
        db.tables = db.tables.filter((x) => x !== t);
        save();
      }
      return { statements: [`db.${t.name}.drop()`], applied: !!body.apply };
    },
  ],
  [
    "POST",
    /^\/services\/([^/]+)\/data\/collections\/([^/]+)\/changes$/,
    ({ m, body }): ChangeResult => {
      const t = db.tables.find((x) => x.name === decodeURIComponent(m[2]));
      if (!t) throw notFound();
      return docChanges(t, body.changes as DocChanges, !!body.apply);
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
    /^\/services\/([^/]+)\/data\/key\/changes$/,
    ({ body }): ChangeResult => keyChanges(String(body.key), body.changes as KeyChanges, !!body.apply),
  ],
  [
    "POST",
    /^\/services\/([^/]+)\/data\/console$/,
    ({ m, body }) => {
      const kind = serviceOf(m[1]).kind;
      if (kind === "redis") return redisConsole(String(body.query));
      if (kind === "mongodb") return { output: "This demo runs no real MongoDB. Browse shows its documents." };
      return pgConsole(String(body.query));
    },
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
            return (dbDemo[d?.kind ?? "postgres"] ?? dbDemo.postgres).url(name, "********");
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
      return out instanceof Response ? out : json(200, out);
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
