import { createPasskey, getPasskey, type CreationOptionsJSON, type RequestOptionsJSON } from "./lib/webauthn";

export type DatabaseKind = "postgres" | "redis";
export type ServiceKind = "app" | DatabaseKind;

export const isDatabase = (kind: ServiceKind): kind is DatabaseKind => kind !== "app";

/** Whether a service has data to back up: a database, or an app with volumes. */
export const canBackup = (s: Pick<Service, "kind" | "volumes">) => isDatabase(s.kind) || s.volumes.length > 0;

/** Whether Traefik routes the service's domain: an app with published ports may have a domain only for DNS. */
export const httpRouted = (s: Pick<Service, "domain" | "port">) => !!s.domain && s.port > 0;

export const databasePorts: Record<DatabaseKind, number> = { postgres: 5432, redis: 6379 };

/** A project database, as env references see it. */
export type DatabaseRef = { name: string; kind: DatabaseKind };

export type Project = {
  id: string;
  name: string;
  serverId: string;
  /** Shared variables and secrets; services reference them as {{ project.NAME }}. */
  env: Record<string, string>;
  /** Names of the env entries that are secrets: their values come back masked. */
  secrets: string[];
  createdAt: string;
  updatedAt: string;
};

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
  /** A database of a git project that the compose file no longer lists. */
  orphaned: boolean;
  replicas: number;
  port: number;
  domain: string;
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
  /** Seconds an app gets to exit after SIGTERM; 0 means 10. */
  stopGraceSeconds: number;
  /** Runs in the host's network: one replica, no domain (apps only). */
  hostNetwork: boolean;
  /** The host's Docker socket mounted read-only or read-write; "" when not. */
  dockerSocket: "" | "ro" | "rw";
  /** IDs of the networks created by hand it joins, besides its project network. */
  networks: string[];
  currentDeploymentId: string;
  stopped: boolean;
  createdAt: string;
  updatedAt: string;
  containers: Container[];
  /** Set when the manager manages the domain's Cloudflare DNS record. */
  dns?: DNSStatus;
};

export type Volume = { name: string; path: string };

export type PublishedPort = { hostPort: number; containerPort: number; protocol: "tcp" | "udp" };

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

/** A container with the networks it is attached to. */
export type TopoNode = Container & {
  endpoints: { network: string; ip?: string; aliases?: string[] }[];
};

export type TopoService = {
  id: string;
  name: string;
  kind: ServiceKind;
  image: string;
  icon?: string;
  domain?: string;
  port?: number;
  replicas: number;
  stopped?: boolean;
  /** Volume holding a database's data. */
  volume?: string;
  /** IDs of the project databases its env references. */
  uses: string[];
  /** IDs of the networks created by hand it joins. */
  networks: string[];
  hostNetwork?: boolean;
  containers: TopoNode[];
};

export type TopoNetwork = {
  name: string;
  subnet?: string;
  gateway?: string;
  /** Empty for the proxy network and the ones created by hand. */
  projectId?: string;
  /** Name and ID of a network created by hand. */
  custom?: string;
  customId?: string;
  /** Expected but not found on the server. */
  missing?: boolean;
};

/** gitPath: the compose file the project follows, when it's linked to git. */
export type TopoProject = { id: string; name: string; network: string; gitPath?: string; services: TopoService[] };

export type ServerTopology = {
  id: string;
  name: string;
  kind: "local" | "ssh";
  /** Set when the server's Docker could not be read. */
  error?: string;
  /** False when routing is left to the user's own proxy. */
  traefik: boolean;
  tunnel: boolean;
  entrypoints: { name: string; port: number; hostPort?: string; redirectTo?: string }[];
  proxy?: TopoNode;
  cloudflared?: TopoNode;
  manager?: { domain?: string; upstream: string; container?: TopoNode };
  networks: TopoNetwork[];
  projects: TopoProject[];
};

export type Topology = { servers: ServerTopology[] };

export type ServiceInput = {
  name: string;
  kind?: ServiceKind;
  image?: string;
  icon?: string;
  replicas?: number;
  port?: number;
  domain?: string;
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
  stopGraceSeconds?: number;
  hostNetwork?: boolean;
  dockerSocket?: "" | "ro" | "rw";
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
  status: "running" | "succeeded" | "failed";
  image: string;
  gitCommit?: string;
  /** user:<name> or token:<name>. */
  triggeredBy?: string;
  error?: string;
  createdAt: string;
  finishedAt?: string;
};

export type OpStatus = "running" | "succeeded" | "failed";

export type TargetKindName = "local" | "s3";

export type BackupTarget = {
  id: string;
  name: string;
  kind: TargetKindName;
  endpoint: string;
  region: string;
  bucket: string;
  prefix: string;
  accessKey: string;
  secretKey: string;
  useSsl: boolean;
  ageRecipient: string;
  createdAt: string;
};

export type TargetInput = Omit<BackupTarget, "id" | "kind" | "createdAt" | "ageRecipient"> & {
  kind?: TargetKindName;
  encrypt?: boolean;
};

export type BackupKind = "postgres" | "volume" | "manager";

export type Backup = {
  id: string;
  /** postgres: a pg_dump; volume: an archive of a service's volumes. */
  kind: BackupKind;
  /** The PostgreSQL database dumped; absent on older backups (the service's own). */
  database?: string;
  encrypted: boolean;
  serviceId: string;
  projectId: string;
  serviceName: string;
  projectName: string;
  /** The backed-up service's kind; absent for the manager's own backups. */
  serviceKind?: ServiceKind;
  /** The service's icon hint (its icon, else its image's base name). */
  serviceIcon?: string;
  targetId: string;
  objectKey: string;
  status: OpStatus;
  sizeBytes: number;
  sha256: string;
  pgVersion: string;
  /** The volumes in a volume backup's archive. */
  volumes?: string[];
  durationMs: number;
  error?: string;
  createdAt: string;
  finishedAt?: string;
  verifyStatus?: OpStatus;
  verifyError?: string;
  verifyDetails: { tables: number; rows: number; dbBytes: number; files?: number; durationMs: number };
  verifiedAt?: string;
};

export type ScheduleInput = {
  targetId: string;
  /** One of a PostgreSQL instance's databases; empty follows the service's own. */
  database?: string;
  cron: string;
  keepLast: number;
  keepDaily: number;
  keepWeekly: number;
  keepMonthly: number;
  enabled: boolean;
  verify: boolean;
};

export type Schedule = ScheduleInput & {
  id: string;
  kind: BackupKind;
  serviceId?: string;
  createdAt: string;
  nextRun?: string;
};

export type Restore = {
  id: string;
  backupId: string;
  serviceId: string;
  status: OpStatus;
  error?: string;
  createdAt: string;
  finishedAt?: string;
};

export type Server = {
  id: string;
  name: string;
  kind: "local" | "ssh";
  host: string;
  port: number;
  sshUser: string;
  socket: string;
  hostKey: string;
  projects: number;
  docker?: { version: string; os: string; arch: string };
  dockerError?: string;
  /** Where Cloudflare DNS records point; empty means detected (detectedIp). */
  publicIp: string;
  detectedIp?: string;
  /** Source of its Cloudflare tunnel token: set in the UI, or the manager's env. */
  tunnel?: "server" | "env";
};

export type ServerNetwork = { publicIp: string; tunnelToken: string };

export type ServerInput = {
  name: string;
  host: string;
  port: number;
  sshUser: string;
  socket: string;
  resetHostKey?: boolean;
};

export type Scope = "read" | "deploy" | "admin";

export type ApiToken = { id: string; name: string; scope: Scope; createdAt: string; lastUsedAt?: string };

/** Credentials pulls from one registry host use; password reads back masked. */
export type Registry = { id: string; host: string; username: string; password: string; createdAt: string };

export type RegistryInput = Pick<Registry, "host" | "username" | "password">;

/** A service on a network created by hand, reached there as its alias. */
export type NetworkMember = { id: string; name: string; kind: ServiceKind; projectId: string; projectName: string; alias: string };

/** Where a map canvas node was dragged to, relative to its parent frame. */
export type CanvasPoint = { x: number; y: number };

/** A network created by hand on a server, which services of any project there can join. */
export type Network = { id: string; serverId: string; name: string; dockerName: string; services: NetworkMember[]; createdAt: string };

export type AuditEntry = {
  id: string;
  actor: string;
  action: string;
  target: string;
  status: number;
  error?: string;
  createdAt: string;
};

export type LogLine = { container: string; time: string; text: string };

export type User = { id: string; username: string; createdAt: string };

/** A password sign-in that still needs a TOTP or recovery code. */
export type MfaChallenge = { mfaRequired: true; ticket: string };

export type Passkey = { id: string; name: string; createdAt: string; lastUsedAt?: string };

export type Account = { username: string; totpEnabled: boolean; recoveryCodes: number; passkeys: Passkey[] };

export type TotpSetup = { secret: string; uri: string };

type Ceremony<T> = { ceremony: string; options: T };

export type AuthState = { setupRequired: boolean; user?: User };

/** A base domain offered when giving a service a public domain. */
export type Domain = {
  id: string;
  name: string;
  proxied: boolean;
  createdAt: string;
  /** In a zone the connected Cloudflare token manages. */
  cloudflare?: boolean;
  /** The zone's SSL/TLS mode: off, flexible, full or strict. */
  sslMode?: string;
};

export type Cloudflare = {
  connected: boolean;
  zones: string[];
  error?: string;
  tunnels: { server: string; id: string }[];
  tunnelError?: string;
  syncedAt?: string;
};

/** A password manager whose references (scheme://…) service env can use. */
export type SecretProvider = {
  id: string;
  name: string;
  scheme: string;
  tokenLabel: string;
  example: string;
  /** How to get a token; `code` spans are commands. */
  help: string;
  /** False when it can't run on the manager (its CLI is missing). */
  available: boolean;
  connected: boolean;
};

/** An item of a password manager vault, with the fields env can reference. */
export type SecretItem = { title: string; fields: { name: string; ref: string }[] };

export type CleanupSettings = {
  enabled: boolean;
  cron: string;
  minAgeHours: number;
  images: "off" | "dangling" | "unused";
  volumes: "off" | "anonymous" | "unused";
  buildCache: boolean;
  containers: boolean;
  networks: boolean;
  /** Deployments kept per service; 0 keeps all. */
  keepDeployments: number;
};

export type CleanupResult = {
  server: string;
  containers: number;
  images: number;
  volumes: number;
  networks: number;
  buildCache: number;
  /** Approximate: images share layers. */
  reclaimed: number;
  errors?: string[];
};

export type Cleanup = {
  settings: CleanupSettings;
  running: boolean;
  nextRun?: string;
  lastRun?: {
    trigger: "schedule" | "manual";
    startedAt: string;
    finishedAt?: string;
    servers: CleanupResult[];
    deployments: number;
    error?: string;
  };
};

/** A delivery method (Discord…); its fields describe the config form. */
export type NotificationKind = {
  name: string;
  label: string;
  fields: { key: string; label: string; placeholder?: string; required: boolean; secret: boolean }[];
};

export type NotificationChannel = {
  id: string;
  name: string;
  kind: string;
  /** Secret values come back masked. */
  config: Record<string, string>;
  events: string[];
  enabled: boolean;
  createdAt: string;
};

export type ChannelInput = Pick<NotificationChannel, "name" | "kind" | "config" | "events" | "enabled">;

export type Notifications = {
  channels: NotificationChannel[];
  kinds: NotificationKind[];
  events: { type: string; label: string; default: boolean }[];
};

/** State of a service domain's managed Cloudflare record. */
export type DNSStatus = { state: "synced" | "conflict" | "error"; message?: string };

export type UpdateInfo = {
  current: string;
  latest?: string;
  available: boolean;
  /** False when the manager can't replace itself; reason says why. */
  canApply: boolean;
  reason?: string;
  updating: boolean;
  checkedAt?: string;
  error?: string;
};

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
};

/** Secret values come back masked; sending the mask keeps the stored value. */
export const SECRET_MASK = "********";

export interface ComposePlan {
  create: string[];
  update: { name: string; fields: string[] }[];
  unchanged: string[];
  delete: string[];
  orphaned: string[];
  variables: string[];
  warnings: string[];
  deploying: string[];
}

/** A one-click app: a compose file and the inputs its variables come from. */
export interface AppTemplate {
  id: string;
  title: string;
  description: string;
  website?: string;
  docs?: string;
  icon?: string;
  inputs: TemplateInput[];
  /** The default project name. */
  project: string;
  compose: string;
}

export interface TemplateInput {
  name: string;
  label: string;
  type: "text" | "secret" | "domain" | "url";
  help?: string;
  placeholder?: string;
  default?: string;
  required?: boolean;
  /** Random bytes the manager generates; not asked. */
  generate?: number;
}

export interface TemplateInstall {
  projectId?: string;
  newProject?: { name: string; serverId?: string };
  values: Record<string, string>;
  dryRun?: boolean;
}

export interface TemplateResult {
  projectId?: string;
  plan: ComposePlan;
}

export interface GitInput {
  repoUrl: string;
  branch: string;
  path: string;
  /** The git provider reading a private repository; "" for a public one. */
  providerId: string;
  autoSync: boolean;
  pollSeconds: number;
}

export interface GitStatus extends GitInput {
  projectId: string;
  webhookSecret: string;
  webhookPath: string;
  lastCommit: string;
  lastSyncedAt?: string;
  lastError: string;
  warnings: string[];
  applied: Record<string, string>;
  /** Services deployed with another image than the file's (a tag from CI). */
  drift: string[];
}

export type GitProviderKind = "github" | "gitlab" | "gitea";

/** Read access to a forge account's repositories: a GitHub App installation or a GitLab/Gitea OAuth authorization. */
export interface GitProvider {
  id: string;
  kind: GitProviderKind;
  name: string;
  baseUrl: string;
  /** The account it's installed on or authorized by. */
  account: string;
  appSlug?: string;
  clientId?: string;
  /** Masked. */
  clientSecret?: string;
  connected: boolean;
  /** Pushes reach the manager through the app, for every repository. */
  webhooks: boolean;
  createdAt: string;
}

export interface GitProviderInput {
  kind?: GitProviderKind;
  name: string;
  baseUrl: string;
  clientId: string;
  clientSecret: string;
}

export interface GitRepo {
  fullName: string;
  cloneUrl: string;
  defaultBranch: string;
  private: boolean;
}

/** A database of a postgres instance; main is the service's own (POSTGRES_DB). */
export type PgDatabase = { name: string; bytes: number; main: boolean };
/** Data browser: a postgres table or view. rowEstimate is -1 until the table is analyzed. */
export type PgTable = { schema: string; name: string; kind: string; rowEstimate: number; bytes: number; columns: PgColumn[] };
export type PgColumn = { name: string; type: string; nullable: boolean; primaryKey: boolean };
export type PgFilterOp = "=" | "!=" | "<" | "<=" | ">" | ">=" | "like" | "null" | "notnull";
export type PgFilter = { column: string; op: PgFilterOp; value?: string };
export type RowQuery = { limit?: number; offset?: number; orderBy?: string; desc?: boolean; filters?: PgFilter[]; search?: string };
/** Cells are text, null for NULL; truncated lists the [row, column] cells cut short. */
export type PgRows = { columns: PgColumn[]; rows: (string | null)[][]; truncated: [number, number][]; hasMore: boolean };
/** ttl in ms, -1 when the key never expires. */
export type RedisKey = { key: string; type: string; ttl: number; bytes: number };
/** cursor "0" once the scan is complete. */
export type RedisKeys = { keys: RedisKey[]; cursor: string };
/** items by type: [value], [field, value], [index, value], [member], [member, score], [id, field, value, ...]. */
export type RedisValue = { type: string; length: number; ttl: number; items: string[][]; truncated: [number, number][]; cursor: string };
export type ConsoleResult = {
  columns?: string[];
  rows?: (string | null)[][];
  truncated?: [number, number][];
  more?: boolean;
  output?: string;
};

const rowParams = (q: RowQuery, database: string) => {
  const p = new URLSearchParams();
  if (database) p.set("database", database);
  if (q.limit) p.set("limit", String(q.limit));
  if (q.offset) p.set("offset", String(q.offset));
  if (q.orderBy) p.set("order", q.orderBy);
  if (q.desc) p.set("desc", "true");
  if (q.filters?.length) p.set("filters", JSON.stringify(q.filters));
  if (q.search) p.set("search", q.search);
  return p.toString();
};

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/api${path}`, {
    ...init,
    headers: { "Content-Type": "application/json", ...init?.headers },
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new ApiError(res.status, body.error ?? res.statusText);
  }
  if (res.status === 204) return undefined as T;
  return res.headers.get("Content-Type")?.includes("json") ? res.json() : (res.text() as T);
}

const json = (method: string, body: unknown): RequestInit => ({ method, body: JSON.stringify(body) });

const composePath = (id: string, service?: string) =>
  `/projects/${id}/compose${service ? `?service=${encodeURIComponent(service)}` : ""}`;

export const api = {
  authState: () => request<AuthState>("/auth/state"),
  setup: (setupToken: string, username: string, password: string) =>
    request<User>("/auth/setup", json("POST", { setupToken, username, password })),
  login: (username: string, password: string) =>
    request<User | MfaChallenge>("/auth/login", json("POST", { username, password })),
  loginMfa: (ticket: string, code: string) => request<User>("/auth/login/mfa", json("POST", { ticket, code })),
  loginPasskey: async () => {
    const { ceremony, options } = await request<Ceremony<RequestOptionsJSON>>("/auth/passkey/begin", json("POST", {}));
    const credential = await getPasskey(options);
    return request<User>("/auth/passkey/finish", json("POST", { ceremony, credential }));
  },
  logout: () => request<void>("/auth/logout", { method: "POST" }),

  account: () => request<Account>("/account"),
  changePassword: (current: string, password: string) => request<void>("/account/password", json("PUT", { current, password })),
  beginTotp: (password: string) => request<TotpSetup>("/account/totp", json("POST", { password })),
  enableTotp: (code: string) => request<{ recoveryCodes: string[] }>("/account/totp/enable", json("POST", { code })),
  disableTotp: (password: string) => request<void>("/account/totp/disable", json("POST", { password })),
  regenerateRecoveryCodes: (password: string) =>
    request<{ recoveryCodes: string[] }>("/account/recovery-codes", json("POST", { password })),
  /** Asks for the password, then the browser's passkey prompt. */
  addPasskey: async (password: string, name: string) => {
    const { ceremony, options } = await request<Ceremony<CreationOptionsJSON>>("/account/passkeys/begin", json("POST", { password }));
    const credential = await createPasskey(options);
    return request<Passkey>("/account/passkeys", json("POST", { ceremony, name, credential }));
  },
  deletePasskey: (id: string) => request<void>(`/account/passkeys/${id}`, { method: "DELETE" }),

  status: () => request<Status>("/status"),
  applyUpdate: () => request<UpdateInfo>("/update", { method: "POST" }),
  checkUpdate: () => request<UpdateInfo>("/update/check", { method: "POST" }),

  projects: () => request<Project[]>("/projects"),
  project: (id: string) => request<Project>(`/projects/${id}`),
  topology: (projectId?: string) => request<Topology>(projectId ? `/projects/${projectId}/topology` : "/topology"),
  createProject: (name: string, serverId = "") => request<Project>("/projects", json("POST", { name, serverId })),
  setProjectEnv: (id: string, env: Record<string, string>, secrets: string[]) =>
    request<Project>(`/projects/${id}/env`, json("PUT", { env, secrets })),
  compose: (id: string, service?: string) => request<string>(composePath(id, service)),
  composeUrl: (id: string, service?: string) => `/api${composePath(id, service)}${service ? "&" : "?"}download=1`,
  applyCompose: (id: string, body: { compose: string; env: string; prune: boolean; dryRun?: boolean; deploy?: boolean }) =>
    request<ComposePlan>(`/projects/${id}/compose`, json("POST", body)),
  templates: () => request<AppTemplate[]>("/templates"),
  installTemplate: (id: string, body: TemplateInstall) => request<TemplateResult>(`/templates/${id}/install`, json("POST", body)),
  /** The project's git link; null when it has none. */
  projectGit: (id: string) =>
    request<GitStatus>(`/projects/${id}/git`).catch((e) => {
      if (e instanceof ApiError && e.status === 404) return null;
      throw e;
    }),
  linkProjectGit: (id: string, input: GitInput) => request<GitStatus>(`/projects/${id}/git`, json("PUT", input)),
  previewProjectGit: (id: string, input: GitInput) => request<ComposePlan>(`/projects/${id}/git/preview`, json("POST", input)),
  unlinkProjectGit: (id: string) => request<void>(`/projects/${id}/git`, { method: "DELETE" }),
  syncProjectGit: (id: string, dryRun = false) =>
    request<ComposePlan>(`/projects/${id}/git/sync${dryRun ? "?dryRun=1" : ""}`, { method: "POST" }),
  /** A zip of compose.yaml and the .env with the secret values. */
  exportBundle: async (id: string, password: string, service?: string) => {
    const res = await fetch(`/api/projects/${id}/export`, {
      ...json("POST", { password, service: service ?? "" }),
      headers: { "Content-Type": "application/json" },
    });
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      throw new ApiError(res.status, body.error ?? res.statusText);
    }
    return res.blob();
  },
  renameProject: (id: string, name: string) => request<Project>(`/projects/${id}/name`, json("PUT", { name })),
  deleteProject: (id: string, confirm: string) =>
    request<void>(`/projects/${id}?confirm=${encodeURIComponent(confirm)}`, { method: "DELETE" }),

  services: (projectId: string) => request<Service[]>(`/projects/${projectId}/services`),
  service: (id: string) => request<Service>(`/services/${id}`),
  createService: (projectId: string, input: ServiceInput) =>
    request<Service>(`/projects/${projectId}/services`, json("POST", input)),
  updateService: (id: string, patch: ServicePatch) => request<Service>(`/services/${id}`, json("PATCH", patch)),
  renameService: (id: string, name: string) => request<Service>(`/services/${id}/name`, json("PUT", { name })),
  setServiceNetworks: (id: string, networks: string[]) => request<Service>(`/services/${id}/networks`, json("PUT", { networks })),
  deleteService: (id: string, confirm = "") =>
    request<void>(`/services/${id}?confirm=${encodeURIComponent(confirm)}`, { method: "DELETE" }),
  connection: (id: string) => request<Connection>(`/services/${id}/connection`),
  serviceStats: (id: string) => request<ServiceStats>(`/services/${id}/stats`),
  uptime: (id: string) => request<Uptime>(`/services/${id}/uptime`),
  setUptime: (id: string, input: UptimeInput) => request<Uptime>(`/services/${id}/uptime`, json("PUT", input)),
  deleteUptime: (id: string) => request<void>(`/services/${id}/uptime`, { method: "DELETE" }),
  /** Current use of every running service, by service ID. */
  usage: () => request<Record<string, ServiceUsage>>("/stats"),
  serviceAction: (id: string, action: "start" | "stop" | "restart") =>
    request<Service>(`/services/${id}/${action}`, { method: "POST" }),

  rollback: (serviceId: string, deploymentId = "") =>
    request<Deployment>(`/services/${serviceId}/rollback`, json("POST", { deploymentId })),
  deploy: (serviceId: string) => request<Deployment>(`/services/${serviceId}/deploy`, { method: "POST" }),
  deployments: (serviceId: string) => request<Deployment[]>(`/services/${serviceId}/deployments`),
  deploymentLog: (id: string) => request<string>(`/deployments/${id}/log`),

  backupTargets: () => request<BackupTarget[]>("/storage"),
  createBackupTarget: (t: TargetInput) => request<BackupTarget>("/storage", json("POST", t)),
  updateBackupTarget: (id: string, t: TargetInput) => request<BackupTarget>(`/storage/${id}`, json("PUT", t)),
  backupTargetKey: (id: string) => request<{ identity: string; recipient: string }>(`/storage/${id}/key`),
  backupManager: (targetId: string) => request<Backup>("/manager/backups", json("POST", { targetId })),
  deleteBackupTarget: (id: string) => request<void>(`/storage/${id}`, { method: "DELETE" }),
  encryptBackupTarget: (id: string) => request<BackupTarget>(`/storage/${id}/encrypt`, { method: "POST" }),
  testBackupTarget: (id: string) => request<void>(`/storage/${id}/test`, { method: "POST" }),

  backups: (projectId?: string) => request<Backup[]>(`/backups${projectId ? `?projectId=${projectId}` : ""}`),
  serviceBackups: (serviceId: string) => request<Backup[]>(`/services/${serviceId}/backups`),
  backup: (serviceId: string, targetId: string, database = "") =>
    request<Backup>(`/services/${serviceId}/backups`, json("POST", { targetId, database })),
  getBackup: (id: string) => request<Backup>(`/backups/${id}`),
  verifyBackup: (id: string) => request<Backup>(`/backups/${id}/verify`, { method: "POST" }),
  deleteBackup: (id: string) => request<void>(`/backups/${id}`, { method: "DELETE" }),
  downloadUrl: (id: string) => `/api/backups/${id}/download`,
  pgDatabases: (serviceId: string) => request<PgDatabase[]>(`/services/${serviceId}/data/databases`),
  createPgDatabase: (serviceId: string, name: string) =>
    request<PgDatabase>(`/services/${serviceId}/data/databases`, json("POST", { name })),
  pgTables: (serviceId: string, database: string) =>
    request<PgTable[]>(`/services/${serviceId}/data/tables?${new URLSearchParams({ database })}`),
  pgRows: (serviceId: string, database: string, schema: string, table: string, q: RowQuery) =>
    request<PgRows>(`/services/${serviceId}/data/tables/${encodeURIComponent(schema)}/${encodeURIComponent(table)}?${rowParams(q, database)}`),
  pgExportUrl: (serviceId: string, database: string, schema: string, table: string, q: RowQuery) =>
    `/api/services/${serviceId}/data/tables/${encodeURIComponent(schema)}/${encodeURIComponent(table)}/export?${rowParams({ ...q, limit: 0, offset: 0 }, database)}`,
  redisScan: (serviceId: string, cursor: string, pattern: string) =>
    request<RedisKeys>(`/services/${serviceId}/data/keys?${new URLSearchParams({ cursor, pattern })}`),
  redisGet: (serviceId: string, key: string, cursor = "0") =>
    request<RedisValue>(`/services/${serviceId}/data/key?${new URLSearchParams({ key, cursor })}`),
  dataConsole: (serviceId: string, query: string, write: boolean, database = "") =>
    request<ConsoleResult>(`/services/${serviceId}/data/console`, json("POST", { query, write, database })),
  restore: (backupId: string, confirm: string, serviceId = "") =>
    request<Restore>(`/backups/${backupId}/restore`, json("POST", { serviceId, confirm })),
  schedules: (serviceId: string) => request<Schedule[]>(`/services/${serviceId}/schedules`),
  createSchedule: (serviceId: string, s: ScheduleInput) =>
    request<Schedule>(`/services/${serviceId}/schedules`, json("POST", s)),
  managerSchedules: () => request<Schedule[]>("/manager/schedules"),
  createManagerSchedule: (s: ScheduleInput) => request<Schedule>("/manager/schedules", json("POST", s)),
  updateSchedule: (id: string, s: ScheduleInput) => request<Schedule>(`/schedules/${id}`, json("PUT", s)),
  deleteSchedule: (id: string) => request<void>(`/schedules/${id}`, { method: "DELETE" }),
  backupProject: (projectId: string, targetId = "") =>
    request<{ backups: Backup[]; error?: string }>(`/projects/${projectId}/backups`, json("POST", { targetId })),
  restores: (serviceId: string) => request<Restore[]>(`/services/${serviceId}/restores`),

  servers: () => request<Server[]>("/servers"),
  createServer: (s: ServerInput) => request<Server>("/servers", json("POST", s)),
  updateServer: (id: string, s: ServerInput) => request<Server>(`/servers/${id}`, json("PUT", s)),
  deleteServer: (id: string) => request<void>(`/servers/${id}`, { method: "DELETE" }),
  networks: () => request<Network[]>("/networks"),
  createNetwork: (n: { serverId: string; name: string }) => request<Network>("/networks", json("POST", n)),
  deleteNetwork: (id: string) => request<void>(`/networks/${id}`, { method: "DELETE" }),
  canvasLayout: () => request<{ positions: Record<string, CanvasPoint> }>("/canvas"),
  saveCanvasLayout: (positions: Record<string, CanvasPoint>) => request<void>("/canvas", json("PUT", { positions })),
  resetCanvasLayout: () => request<void>("/canvas", { method: "DELETE" }),
  gitProviders: () => request<GitProvider[]>("/git-providers"),
  createGitProvider: (p: GitProviderInput) => request<GitProvider>("/git-providers", json("POST", p)),
  updateGitProvider: (id: string, p: GitProviderInput) => request<GitProvider>(`/git-providers/${id}`, json("PUT", p)),
  deleteGitProvider: (id: string) => request<void>(`/git-providers/${id}`, { method: "DELETE" }),
  testGitProvider: (id: string) => request<void>(`/git-providers/${id}/test`, { method: "POST" }),
  /** The forge page that connects the provider (OAuth consent, or the GitHub App's installation). */
  authorizeGitProvider: (id: string) => request<{ url: string }>(`/git-providers/${id}/authorize`, { method: "POST" }),
  /** The manager page that posts the GitHub App manifest to GitHub. */
  startGitHubApp: (input: { name: string; baseUrl: string; org: string }) =>
    request<{ url: string }>("/git-providers/github", json("POST", input)),
  gitProviderRepos: (id: string) => request<GitRepo[]>(`/git-providers/${id}/repos`),
  gitProviderBranches: (id: string, repo: string) =>
    request<string[]>(`/git-providers/${id}/branches?repo=${encodeURIComponent(repo)}`),
  registries: () => request<Registry[]>("/registries"),
  createRegistry: (r: RegistryInput) => request<Registry>("/registries", json("POST", r)),
  updateRegistry: (id: string, r: RegistryInput) => request<Registry>(`/registries/${id}`, json("PUT", r)),
  deleteRegistry: (id: string) => request<void>(`/registries/${id}`, { method: "DELETE" }),
  testRegistry: (id: string) => request<void>(`/registries/${id}/test`, { method: "POST" }),
  sshKey: () => request<{ publicKey: string }>("/ssh-key"),
  domains: () => request<Domain[]>("/domains"),
  createDomain: (name: string) => request<Domain>("/domains", json("POST", { name })),
  deleteDomain: (id: string) => request<void>(`/domains/${id}`, { method: "DELETE" }),
  setDomainProxied: (id: string, proxied: boolean) => request<Domain>(`/domains/${id}`, json("PATCH", { proxied })),
  cloudflare: () => request<Cloudflare>("/cloudflare"),
  connectCloudflare: (token: string) => request<Cloudflare>("/cloudflare", json("PUT", { token })),
  disconnectCloudflare: () => request<void>("/cloudflare", { method: "DELETE" }),
  testCloudflare: () => request<void>("/cloudflare/test", { method: "POST" }),
  secretProviders: () => request<SecretProvider[]>("/secret-providers"),
  connectSecretProvider: (id: string, token: string) =>
    request<SecretProvider>(`/secret-providers/${id}`, json("PUT", { token })),
  /** Listings are cached by the manager for a few minutes, unless refresh. */
  secretVaults: (id: string, refresh = false) =>
    request<string[]>(`/secret-providers/${id}/vaults${refresh ? "?refresh" : ""}`),
  secretItems: (id: string, vault: string, refresh = false) =>
    request<SecretItem[]>(`/secret-providers/${id}/items?vault=${encodeURIComponent(vault)}${refresh ? "&refresh" : ""}`),
  disconnectSecretProvider: (id: string) => request<void>(`/secret-providers/${id}`, { method: "DELETE" }),
  testSecretProvider: (id: string) => request<void>(`/secret-providers/${id}/test`, { method: "POST" }),
  setServerNetwork: (id: string, n: ServerNetwork) => request<Server>(`/servers/${id}/network`, json("PUT", n)),
  cleanup: () => request<Cleanup>("/cleanup"),
  setCleanup: (s: CleanupSettings) => request<Cleanup>("/cleanup", json("PUT", s)),
  runCleanup: () => request<Cleanup>("/cleanup/run", { method: "POST" }),
  notifications: () => request<Notifications>("/notifications"),
  createChannel: (c: ChannelInput) => request<NotificationChannel>("/notifications/channels", json("POST", c)),
  updateChannel: (id: string, c: ChannelInput) =>
    request<NotificationChannel>(`/notifications/channels/${id}`, json("PUT", c)),
  deleteChannel: (id: string) => request<void>(`/notifications/channels/${id}`, { method: "DELETE" }),
  testChannel: (id: string) => request<void>(`/notifications/channels/${id}/test`, { method: "POST" }),
  tokens: () => request<ApiToken[]>("/tokens"),
  createToken: (name: string, scope: Scope) =>
    request<ApiToken & { token: string }>("/tokens", json("POST", { name, scope })),
  deleteToken: (id: string) => request<void>(`/tokens/${id}`, { method: "DELETE" }),
  audit: () => request<AuditEntry[]>("/audit?limit=200"),

  logsUrl: (serviceId: string, tail = 200) => `/api/services/${serviceId}/logs?tail=${tail}`,
  terminalUrl: (serviceId: string, container: string, shell: string) =>
    `/api/services/${serviceId}/terminal?${new URLSearchParams({ container, shell })}`,
  serverTerminalUrl: (serverId: string) => `/api/servers/${serverId}/terminal`,
};
