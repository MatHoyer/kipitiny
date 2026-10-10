import type {
  ApiToken,
  AuditEntry,
  Backup,
  CanvasPoint,
  CleanupSettings,
  Deployment,
  Domain,
  Network,
  NotificationChannel,
  Project,
  Registry,
  Restore,
  Schedule,
  Service,
} from "@/api";

/*
 * What the demo starts with: one project running a small SaaS (a front end
 * served by nginx, a Node.js API on two replicas, PostgreSQL and Redis),
 * with rows in the database and keys in the cache to browse.
 */

export type DemoTable = { schema: string; name: string; columns: { name: string; type: string; primaryKey?: boolean; nullable?: boolean }[]; rows: (string | null)[][] };
export type DemoRedisKey = { key: string; type: "string" | "hash" | "list" | "set" | "zset"; ttl: number; items: string[][] };

export type DemoDb = {
  version: 2;
  projects: Project[];
  services: Omit<Service, "containers">[];
  networks: Omit<Network, "services" | "dockerName">[];
  deployments: Deployment[];
  canvas: Record<string, CanvasPoint>;
  tables: DemoTable[];
  redis: DemoRedisKey[];
  backups: Backup[];
  schedules: Schedule[];
  restores: Restore[];
  domains: Domain[];
  registries: Registry[];
  channels: NotificationChannel[];
  tokens: ApiToken[];
  audit: AuditEntry[];
  cleanup: CleanupSettings;
};

const at = (daysAgo: number, hour = 9) => new Date(Date.UTC(2026, 8, 30, hour) - daysAgo * 86_400_000).toISOString();

/** Deterministic pseudo-random numbers, so the sample data is the same for everyone. */
function rng(seed: number) {
  let s = seed;
  return () => {
    s = (s * 1664525 + 1013904223) % 4294967296;
    return s / 4294967296;
  };
}

export function service(p: Partial<Service> & Pick<Service, "id" | "name" | "kind" | "image">): Omit<Service, "containers"> {
  return {
    projectId: "acme",
    serverId: "local",
    icon: "",
    orphaned: false,
    replicas: 1,
    port: 0,
    domain: "",
    env: {},
    secrets: [],
    memoryMb: 0,
    cpus: 0,
    healthPath: "",
    preDeploy: "",
    volumes: [],
    middlewares: {},
    preBackup: "",
    publishedPorts: [],
    stopGraceSeconds: 0,
    hostNetwork: false,
    dockerSocket: "",
    networks: [],
    maintenance: { enabled: false },
    currentDeploymentId: `dep-${p.id}`,
    stopped: false,
    createdAt: at(30),
    updatedAt: at(2),
    ...p,
  };
}

const first = ["Ada", "Grace", "Linus", "Margaret", "Alan", "Barbara", "Ken", "Radia", "Dennis", "Frances", "Edsger", "Hedy", "Donald", "Karen", "Tim"];
const last = ["Lovelace", "Hopper", "Torvalds", "Hamilton", "Turing", "Liskov", "Thompson", "Perlman", "Ritchie", "Allen", "Dijkstra", "Lamarr", "Knuth", "Sparck Jones", "Berners-Lee"];

function tables(): DemoTable[] {
  const r = rng(42);
  const pick = <T,>(a: T[]) => a[Math.floor(r() * a.length)];
  const plans: [string, string, string][] = [
    ["1", "free", "0"],
    ["2", "starter", "900"],
    ["3", "pro", "2900"],
    ["4", "team", "9900"],
  ];
  const users: (string | null)[][] = [];
  for (let i = 1; i <= 140; i++) {
    const f = pick(first);
    const l = pick(last);
    users.push([
      String(i),
      `${f.toLowerCase()}.${l.toLowerCase().replace(/[^a-z]/g, "")}${i}@example.com`,
      `${f} ${l}`,
      pick(plans)[1],
      r() < 0.08 ? null : at(Math.floor(r() * 400), 8 + Math.floor(r() * 10)),
      at(Math.floor(r() * 500)),
    ]);
  }
  const invoices: (string | null)[][] = [];
  for (let i = 1; i <= 320; i++) {
    const user = users[Math.floor(r() * users.length)];
    const plan = plans.find((p) => p[1] === user[3])!;
    if (plan[2] === "0") continue;
    invoices.push([String(1000 + i), user[0], plan[2], pick(["paid", "paid", "paid", "open", "void"]), at(Math.floor(r() * 365))]);
  }
  return [
    {
      schema: "public",
      name: "users",
      columns: [
        { name: "id", type: "bigint", primaryKey: true },
        { name: "email", type: "text" },
        { name: "name", type: "text" },
        { name: "plan", type: "text" },
        { name: "last_login_at", type: "timestamp with time zone", nullable: true },
        { name: "created_at", type: "timestamp with time zone" },
      ],
      rows: users,
    },
    {
      schema: "public",
      name: "plans",
      columns: [
        { name: "id", type: "integer", primaryKey: true },
        { name: "name", type: "text" },
        { name: "price_cents", type: "integer" },
      ],
      rows: plans,
    },
    {
      schema: "public",
      name: "invoices",
      columns: [
        { name: "id", type: "bigint", primaryKey: true },
        { name: "user_id", type: "bigint" },
        { name: "amount_cents", type: "integer" },
        { name: "status", type: "text" },
        { name: "issued_at", type: "timestamp with time zone" },
      ],
      rows: invoices,
    },
  ];
}

function redis(): DemoRedisKey[] {
  const keys: DemoRedisKey[] = [
    { key: "cache:plans", type: "string", ttl: 3_600_000, items: [['[{"id":1,"name":"free"},{"id":2,"name":"starter"},{"id":3,"name":"pro"},{"id":4,"name":"team"}]']] },
    { key: "queue:emails", type: "list", ttl: -1, items: [["0", '{"to":"ada.lovelace1@example.com","template":"welcome"}'], ["1", '{"to":"grace.hopper7@example.com","template":"invoice"}']] },
    { key: "leaderboard:weekly", type: "zset", ttl: -1, items: [["grace", "1280"], ["linus", "1104"], ["ada", "990"], ["alan", "812"]] },
    { key: "features:beta", type: "set", ttl: -1, items: [["new-dashboard"], ["webhooks"], ["sso"]] },
  ];
  const r = rng(7);
  for (let i = 0; i < 12; i++) {
    const id = Math.floor(r() * 1e12).toString(16);
    keys.push({ key: `session:${id}`, type: "hash", ttl: 600_000 + Math.floor(r() * 86_400_000), items: [["user_id", String(1 + Math.floor(r() * 140))], ["ip", `203.0.113.${Math.floor(r() * 250)}`], ["agent", "Mozilla/5.0"]] });
  }
  for (let i = 0; i < 6; i++) keys.push({ key: `rate:203.0.113.${10 + i}`, type: "string", ttl: 30_000 + i * 5000, items: [[String(3 + i)]] });
  return keys;
}

/** A week of nightly backups, each restore-tested, plus the manager's own. */
function backups(): Backup[] {
  const out: Backup[] = [];
  const r = rng(99);
  const base = { encrypted: false, projectId: "acme", projectName: "acme", targetId: "local", status: "succeeded" as const, sha256: "", pgVersion: "" };
  for (let d = 0; d < 7; d++) {
    const created = at(d, 3);
    const db = 4_100_000 + Math.floor(r() * 400_000) + (7 - d) * 30_000;
    out.push({
      ...base,
      id: `bk-db-${d}`,
      kind: "postgres",
      database: "postgres",
      serviceId: "db",
      serviceName: "db",
      serviceKind: "postgres",
      serviceIcon: "postgres",
      objectKey: `acme/db/${created.slice(0, 10)}.dump`,
      sizeBytes: db,
      sha256: (d * 7919).toString(16).padStart(8, "0") + "e3b0c44298fc1c149afbf4c8996fb924",
      pgVersion: "17.6",
      durationMs: 1800 + Math.floor(r() * 900),
      createdAt: created,
      finishedAt: created,
      verifyStatus: "succeeded",
      verifyDetails: { tables: 3, rows: 140 + 4 + 296, dbBytes: db * 3, durationMs: 4200 + Math.floor(r() * 1500) },
      verifiedAt: created,
    });
    out.push({
      ...base,
      id: `bk-cache-${d}`,
      kind: "volume",
      serviceId: "cache",
      serviceName: "cache",
      serviceKind: "redis",
      serviceIcon: "redis",
      objectKey: `acme/cache/${created.slice(0, 10)}.tar.gz`,
      sizeBytes: 180_000 + Math.floor(r() * 40_000),
      volumes: ["kipitiny-cache-data"],
      durationMs: 600 + Math.floor(r() * 300),
      createdAt: created,
      finishedAt: created,
      verifyStatus: "succeeded",
      verifyDetails: { tables: 0, rows: 0, dbBytes: 0, files: 3, durationMs: 900 },
      verifiedAt: created,
    });
  }
  out.push({
    ...base,
    id: "bk-manager-0",
    kind: "manager",
    serviceId: "",
    serviceName: "",
    projectId: "",
    projectName: "",
    objectKey: "manager/2026-09-30.db",
    sizeBytes: 524_288,
    durationMs: 120,
    createdAt: at(0, 4),
    finishedAt: at(0, 4),
    verifyDetails: { tables: 0, rows: 0, dbBytes: 0, durationMs: 0 },
  });
  return out;
}

export function seed(): DemoDb {
  const services = [
    service({ id: "web", name: "web", kind: "app", image: "nginx:1.27-alpine", domain: "acme.example", port: 80 }),
    service({
      id: "api",
      name: "api",
      kind: "app",
      image: "node:22-alpine",
      domain: "api.acme.example",
      port: 3000,
      replicas: 2,
      healthPath: "/healthz",
      preDeploy: "npm run migrate",
      env: { NODE_ENV: "production", DATABASE_URL: "{{ db.db.URL }}", REDIS_URL: "{{ db.cache.URL }}", SESSION_SECRET: "s3cr3t" },
      secrets: ["SESSION_SECRET"],
      volumes: [{ name: "uploads", path: "/app/uploads" }],
    }),
    service({
      id: "db",
      name: "db",
      kind: "postgres",
      image: "postgres:17-alpine",
      memoryMb: 512,
      env: { POSTGRES_USER: "app", POSTGRES_PASSWORD: "demo-password", POSTGRES_DB: "postgres" },
      secrets: ["POSTGRES_PASSWORD"],
    }),
    service({ id: "cache", name: "cache", kind: "redis", image: "redis:8-alpine", memoryMb: 256, env: { REDIS_PASSWORD: "demo-password" }, secrets: ["REDIS_PASSWORD"] }),
  ];
  return {
    version: 2,
    projects: [{ id: "acme", name: "acme", serverId: "local", env: { APP_URL: "https://acme.example" }, secrets: [], createdAt: at(30), updatedAt: at(2) }],
    services,
    networks: [],
    deployments: services.map((s) => ({ id: `dep-${s.id}`, serviceId: s.id, status: "succeeded", image: s.image, triggeredBy: "user:demo", createdAt: at(2, 14), finishedAt: at(2, 14) })),
    canvas: {},
    tables: tables(),
    redis: redis(),
    backups: backups(),
    schedules: [
      { id: "sch-db", kind: "postgres", serviceId: "db", targetId: "local", cron: "0 3 * * *", keepLast: 0, keepDaily: 7, keepWeekly: 4, keepMonthly: 6, enabled: true, verify: true, createdAt: at(30), nextRun: at(-1, 3) },
      { id: "sch-cache", kind: "volume", serviceId: "cache", targetId: "local", cron: "0 3 * * *", keepLast: 7, keepDaily: 0, keepWeekly: 0, keepMonthly: 0, enabled: true, verify: true, createdAt: at(30), nextRun: at(-1, 3) },
      { id: "sch-manager", kind: "manager", targetId: "local", cron: "0 4 * * *", keepLast: 14, keepDaily: 0, keepWeekly: 0, keepMonthly: 0, enabled: true, verify: false, createdAt: at(30), nextRun: at(-1, 4) },
    ],
    restores: [{ id: "rs-1", backupId: "bk-db-3", serviceId: "db", status: "succeeded", createdAt: at(3, 11), finishedAt: at(3, 11) }],
    domains: [{ id: "dom-1", name: "acme.example", proxied: false, createdAt: at(30) }],
    registries: [{ id: "reg-1", host: "ghcr.io", username: "acme-bot", password: "********", createdAt: at(20) }],
    channels: [
      { id: "ch-1", name: "#ops", kind: "discord", config: { webhookUrl: "********" }, events: ["deploy.failed", "backup.failed", "uptime.down", "service.restarted", "service.unhealthy"], enabled: true, createdAt: at(20) },
    ],
    tokens: [
      { id: "tok-1", name: "github-actions", scope: "deploy", createdAt: at(25), lastUsedAt: at(2, 14) },
      { id: "tok-2", name: "claude-code", scope: "read", createdAt: at(10), lastUsedAt: at(0, 10) },
    ],
    audit: [
      { id: "au-1", actor: "token:github-actions", action: "POST /api/services/{id}/deploy", target: "api", status: 202, createdAt: at(2, 14) },
      { id: "au-2", actor: "token:github-actions", action: "POST /api/services/{id}/deploy", target: "web", status: 202, createdAt: at(2, 14) },
      { id: "au-3", actor: "user:demo", action: "POST /api/backups/{id}/restore", target: "bk-db-3", status: 202, createdAt: at(3, 11) },
      { id: "au-4", actor: "user:demo", action: "PATCH /api/services/{id}", target: "api", status: 200, createdAt: at(4, 16) },
      { id: "au-5", actor: "token:claude-code", action: "POST /api/services/{id}/deploy", target: "api", status: 403, error: "this token's scope (read) does not allow that", createdAt: at(5, 10) },
      { id: "au-6", actor: "user:demo", action: "POST /api/projects/{id}/services", target: "acme", status: 201, createdAt: at(30, 9) },
    ],
    cleanup: { enabled: true, cron: "0 5 * * 0", minAgeHours: 24, images: "unused", volumes: "anonymous", buildCache: true, containers: true, networks: true, keepDeployments: 20 },
  };
}
