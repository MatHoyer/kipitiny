import type { OpStatus } from "@/features/backups/types";
import type { DNSStatus, UpdateInfo } from "@/features/settings/types";

export type DatabaseKind = "postgres" | "redis" | "mysql" | "mariadb" | "mongodb";

export type ServiceKind = "app" | DatabaseKind;

export const isDatabase = (kind: ServiceKind): kind is DatabaseKind => kind !== "app";

/** Whether a service has data to back up: a database, or an app with volumes. */
export const canBackup = (s: Pick<Service, "kind" | "volumes">) => isDatabase(s.kind) || s.volumes.length > 0;

/** Whether Traefik routes the service's domain: an app with published ports may have a domain only for DNS. */
export const httpRouted = (s: Pick<Service, "domain" | "port">) => !!s.domain && s.port > 0;

export const databasePorts: Record<DatabaseKind, number> = { postgres: 5432, redis: 6379, mysql: 3306, mariadb: 3306, mongodb: 27017 };

/** The variable apps usually read a database's connection string from. */
export const databaseUrlKey = (kind: DatabaseKind) => (kind === "redis" ? "REDIS_URL" : kind === "mongodb" ? "MONGODB_URI" : "DATABASE_URL");

/** A project database, as env references see it. */
export type DatabaseRef = { name: string; kind: DatabaseKind };

export type Container = {
  id: string;
  name: string;
  replica: number;
  deployId: string;
  image: string;
  state: string;
  status: string;
  health?: string;
  retired?: boolean;
};

export type Service = {
  id: string;
  projectId: string;
  /** The project's server. */
  serverId: string;
  name: string;
  kind: ServiceKind;
  image: string;
  /** Logo name shown in the UI (e.g. ghost); empty picks one from the kind or image. */
  icon: string;
  /** Lowercase names grouping services in the UI; a git project's come from its compose file. */
  tags: string[];
  /** A database of a git project that the compose file no longer lists. */
  orphaned: boolean;
  replicas: number;
  port: number;
  domain: string;
  /** The analytics server counts the domain's visits (when analytics are on). */
  analytics: boolean;
  /** The app's own tracker script and hit paths on its domain (both or neither); empty: the analytics domain's /a.js and /e. */
  analyticsScript?: string;
  analyticsHit?: string;
  /** When the app's public analytics link was created; absent when not shared. */
  analyticsSharedAt?: string;
  /** The analytics server takes the errors of the app's Sentry SDK. */
  errors: boolean;
  env: Record<string, string>;
  /** Names of the env entries that are secrets: their values come back masked. */
  secrets: string[];
  memoryMb: number;
  /** CPU limit in cores; 0 means unlimited. */
  cpus: number;
  healthPath: string;
  preDeploy: string;
  /** Named volumes mounted in every replica (apps only). */
  volumes: Volume[];
  /** Traefik middlewares on the public domain (apps only). */
  middlewares: Middlewares;
  /** Runs (sh -c) in a replica before each volume backup. */
  preBackup: string;
  /** Host ports bound straight to the container (apps only); such an app runs one replica. */
  publishedPorts: PublishedPort[];
  /** Paths of the domain routed to the app, each with its scheme and port; empty routes the whole domain. */
  routes: Route[];
  /** Seconds an app gets to exit after SIGTERM; 0 means 10. */
  stopGraceSeconds: number;
  /** Stopped by the manager for its project being idle; the next request starts it. */
  sleeping: boolean;
  /** Runs in the host's network: one replica, no domain (apps only). */
  hostNetwork: boolean;
  /** The host's Docker socket mounted read-only or read-write; "" when not. */
  dockerSocket: "" | "ro" | "rw";
  /** "only": in the project's previews alone, never deployed here; "skip": left out of previews. */
  preview: "" | "only" | "skip";
  /** Command, entrypoint, user, tmpfs… (apps only); empty fields keep the image's. */
  runtime: Runtime;
  /** Written into the containers before they start: compose configs, secrets, files from git. */
  files: ServiceFile[];
  /** Services of the project started before this one. */
  dependsOn: Dependency[];
  /** The project's own networks it's on, by compose name; empty is the default one. */
  projectNetworks: string[];
  /** IDs of the networks created by hand it joins, besides its project networks. */
  networks: string[];
  currentDeploymentId: string;
  stopped: boolean;
  createdAt: string;
  updatedAt: string;
  containers: Container[];
  /** Set when the manager manages the domain's Cloudflare DNS record. */
  dns?: DNSStatus;
  /** The domain or port changed since the running deployment: Traefik routes the new one after a deploy. */
  routePending?: boolean;
  /** The networks its running containers miss, e.g. one removed outside kipitiny; the reconciler attaches them again. */
  networkIssues?: string[];
};

export type Volume = {
  name: string;
  path: string;
  readOnly?: boolean;
  /** An existing Docker volume mounted as is (admins only). */
  external?: string;
  /** The local driver's options (an NFS share…; admins only). */
  driverOpts?: Record<string, string>;
};

export type Route = {
  /** Path prefixes, e.g. /api or /my.grpc.Service/. */
  paths: string[];
  /** Absent is HTTP/1.1; h2c is HTTP/2 without TLS, for gRPC. */
  scheme?: "h2c";
  /** Container port; absent is the app's. */
  port?: number;
};

export type PublishedPort = {
  /** The server address bound; absent is every address. */
  hostIp?: string;
  hostPort: number;
  containerPort: number;
  protocol: "tcp" | "udp";
};

export type Maintenance = {
  /** Maintenance mode: every visitor but allowIps gets the page while the replicas keep running. */
  enabled: boolean;
  allowIps?: string[];
  /** Replace the default page's heading and text; empty keeps them. */
  title?: string;
  message?: string;
  /** A whole HTML document served instead of the default page. */
  html?: string;
  /** The page visitors get while the project wakes from sleep. */
  starting?: StartingPage;
};

/** Replaces the default "Starting…" page's text, or the whole page with html; empty keeps the default. */
export type StartingPage = { title?: string; message?: string; html?: string };

export type RateLimit = { average: number; burst: number };

export type Middlewares = {
  /** Users with a stored password show only their name; ref is a password manager reference. */
  basicAuth?: { name: string; ref?: string }[];
  ipAllowList?: string[];
  rateLimit?: RateLimit;
  /** Response headers; an empty value removes the header. */
  headers?: Record<string, string>;
};

export type MiddlewaresInput = {
  /** password: a new password, a {{ scheme://… }} reference, or empty to keep the current one. */
  basicAuth: { name: string; password: string }[];
  ipAllowList: string[];
  rateLimit: RateLimit | null;
  headers: Record<string, string>;
};

/** How an app's container runs beyond its image: compose's command, user, tmpfs… */
export type Runtime = {
  /** Replaces the image's CMD. */
  command?: string[];
  /** Replaces the image's ENTRYPOINT; [""] clears it. */
  entrypoint?: string[];
  user?: string;
  workingDir?: string;
  /** Added to the container's labels; never kipitiny.* or traefik.*. */
  labels?: Record<string, string>;
  /** In-memory mounts: path → options (size=64m). */
  tmpfs?: Record<string, string>;
  shmSizeMb?: number;
  ulimits?: Record<string, { soft: number; hard: number }>;
  sysctls?: Record<string, string>;
  readOnly?: boolean;
  init?: boolean;
  stopSignal?: string;
  tty?: boolean;
  stdinOpen?: boolean;
  /** host:ip entries for /etc/hosts. */
  extraHosts?: string[];
  /** Loosen the container's isolation; only admins set them. */
  capAdd?: string[];
  capDrop?: string[];
  devices?: string[];
  privileged?: boolean;
  securityOpt?: string[];
  /** Host paths mounted, as /host:/container[:ro] (admins only). */
  binds?: string[];
  /** Memory Docker tries to leave it when the server runs short. */
  memoryReservationMb?: number;
  /** "no" (a job, run once) or "on-failure[:retries]"; unset keeps it running. */
  restart?: string;
  /** Image platform pulled and run (linux/arm64); unset is the server's own. */
  platform?: string;
};

/** A service started before another: "healthy" waits until it's ready, "completed" until it exits 0. */
export type Dependency = { service: string; condition?: "healthy" | "completed" };

/** A file written into an app's containers; responses leave its content out. */
export type ServiceFile = {
  path: string;
  size: number;
  mode?: number;
  /** A compose secret: its references resolve at deploy. */
  secret?: boolean;
  /** config:NAME, secret:NAME, or the repository path it was mounted from. */
  source?: string;
};

export type ServiceInput = {
  name: string;
  kind?: ServiceKind;
  image?: string;
  icon?: string;
  replicas?: number;
  port?: number;
  domain?: string;
  analytics?: boolean;
  analyticsScript?: string;
  analyticsHit?: string;
  errors?: boolean;
  env?: Record<string, string>;
  secrets?: string[];
  memoryMb?: number;
  cpus?: number;
  healthPath?: string;
  preDeploy?: string;
  volumes?: Volume[];
  middlewares?: MiddlewaresInput;
  preBackup?: string;
  publishedPorts?: PublishedPort[];
  routes?: Route[];
  stopGraceSeconds?: number;
  hostNetwork?: boolean;
  dockerSocket?: "" | "ro" | "rw";
  runtime?: Runtime;
  preview?: "" | "only" | "skip";
};

export type Connection = {
  host: string;
  port: number;
  /** Absent for redis. */
  database?: string;
  user: string;
  password: string;
  url: string;
};

export type ServicePatch = Partial<Omit<ServiceInput, "name">>;

export type Deployment = {
  id: string;
  serviceId: string;
  /** skipped: not tried, a service it depends on failed (compose). */
  status: "running" | "succeeded" | "failed" | "skipped";
  image: string;
  gitCommit?: string;
  /** user:<name> or token:<name>. */
  triggeredBy?: string;
  error?: string;
  createdAt: string;
  finishedAt?: string;
};

export type JobInput = {
  name: string;
  /** Run with sh -c in the app's image. */
  command: string;
  cron: string;
  /** 0: one hour. */
  timeoutSec: number;
  enabled: boolean;
};

export type JobRun = {
  id: string;
  jobId: string;
  status: OpStatus;
  /** Missing when the command never ran or was killed. */
  exitCode?: number;
  /** Empty for the schedule. */
  triggeredBy?: string;
  error?: string;
  createdAt: string;
  finishedAt?: string;
};

export type Job = JobInput & {
  id: string;
  serviceId: string;
  createdAt: string;
  nextRun?: string;
  lastRun?: JobRun;
};

export type LogLine = { container: string; time: string; text: string };

/** A service's resource use at one moment, summed over its running replicas. */
export type Usage = {
  at: string;
  replicas: number;
  /** Percent of one core: 200 is two busy cores. */
  cpu: number;
  memoryBytes: number;
  /** Limit of those replicas together; absent when unlimited. */
  memoryLimitBytes?: number;
  /** Bytes per second. */
  netRx: number;
  netTx: number;
};

export type ServiceStats = {
  /** Null while the service runs no replica. */
  current: Usage | null;
  /** The last five minutes, one point every ten seconds. */
  history: Usage[];
  /** The same for each replica, by short container ID. */
  containers: Record<string, { current: Usage | null; history: Usage[] }>;
};

export type ServiceUsage = Usage & { projectId: string; serverId: string };

export type UptimeCheck = {
  serviceId: string;
  path: string;
  intervalSec: number;
  timeoutSec: number;
  /** 0 accepts any status below 400. */
  expectedStatus: number;
  enabled: boolean;
  /** The state last notified, since changedAt. */
  down: boolean;
  changedAt?: string;
  createdAt: string;
};

export type UptimeInput = Pick<UptimeCheck, "path" | "intervalSec" | "timeoutSec" | "expectedStatus" | "enabled">;

export type UptimeResult = { at: string; ok: boolean; status?: number; latencyMs: number; error?: string };

export type Uptime = {
  /** Null when the service has no check. */
  check: UptimeCheck | null;
  url?: string;
  /** Why the check isn't running. */
  problem?: string;
  /** The latest results since the manager started, oldest first. */
  recent: UptimeResult[];
  uptime24h: number | null;
  uptime7d: number | null;
  uptime30d: number | null;
  /** Over the successful checks of the last 24 hours. */
  avgLatencyMs: number | null;
};

export type Status = {
  version: string;
  update: UpdateInfo;
  docker?: { version: string; apiVersion: string; os: string; arch: string };
  dockerError?: string;
  /** The manager's SQLite file and its write-ahead log not yet merged into it. */
  database?: { sizeBytes: number; walBytes: number };
};

/** Secret values come back masked; sending the mask keeps the stored value. */
export const SECRET_MASK = "********";
