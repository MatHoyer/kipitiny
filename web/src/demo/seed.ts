import type { CanvasPoint, Deployment, Network, Project, Service } from "@/api";

/*
 * What the demo starts with: one project running a small SaaS (a front end
 * served by nginx, a Node.js API on two replicas, PostgreSQL and Redis),
 * with rows in the database and keys in the cache to browse.
 */

export type DemoTable = { schema: string; name: string; columns: { name: string; type: string; primaryKey?: boolean; nullable?: boolean }[]; rows: (string | null)[][] };
export type DemoRedisKey = { key: string; type: "string" | "hash" | "list" | "set" | "zset"; ttl: number; items: string[][] };

export type DemoDb = {
  version: 1;
  projects: Project[];
  services: Omit<Service, "containers">[];
  networks: Omit<Network, "services" | "dockerName">[];
  deployments: Deployment[];
  canvas: Record<string, CanvasPoint>;
  tables: DemoTable[];
  redis: DemoRedisKey[];
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
    version: 1,
    projects: [{ id: "acme", name: "acme", serverId: "local", env: { APP_URL: "https://acme.example" }, secrets: [], createdAt: at(30), updatedAt: at(2) }],
    services,
    networks: [],
    deployments: services.map((s) => ({ id: `dep-${s.id}`, serviceId: s.id, status: "succeeded", image: s.image, triggeredBy: "user:demo", createdAt: at(2, 14), finishedAt: at(2, 14) })),
    canvas: {},
    tables: tables(),
    redis: redis(),
  };
}
