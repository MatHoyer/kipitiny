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
  name: string;
  kind: "app" | "postgres";
  image: string;
  replicas: number;
  port: number;
  domain: string;
  env: Record<string, string>;
  /** Names of the env entries that are secrets: their values come back masked. */
  secrets: string[];
  memoryMb: number;
  healthPath: string;
  preDeploy: string;
  currentDeploymentId: string;
  stopped: boolean;
  source: "image" | "git";
  gitUrl: string;
  gitBranch: string;
  gitToken: string;
  dockerfile: string;
  buildContext: string;
  createdAt: string;
  updatedAt: string;
  containers: Container[];
  /** Set when the manager manages the domain's Cloudflare DNS record. */
  dns?: DNSStatus;
};

/** A container with the networks it is attached to. */
export type TopoNode = Container & {
  endpoints: { network: string; ip?: string; aliases?: string[] }[];
};

export type TopoService = {
  id: string;
  name: string;
  kind: "app" | "postgres";
  image: string;
  domain?: string;
  port?: number;
  replicas: number;
  stopped?: boolean;
  /** Volume holding a database's data. */
  volume?: string;
  /** IDs of the project databases its env references. */
  uses: string[];
  containers: TopoNode[];
};

export type TopoNetwork = {
  name: string;
  subnet?: string;
  gateway?: string;
  /** Empty for the proxy network. */
  projectId?: string;
  /** Expected but not found on the server. */
  missing?: boolean;
};

export type TopoProject = { id: string; name: string; network: string; services: TopoService[] };

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
  kind?: "app" | "postgres";
  image?: string;
  replicas?: number;
  port?: number;
  domain?: string;
  env?: Record<string, string>;
  secrets?: string[];
  memoryMb?: number;
  healthPath?: string;
  preDeploy?: string;
  source?: "image" | "git";
  gitUrl?: string;
  gitBranch?: string;
  gitToken?: string;
  dockerfile?: string;
  buildContext?: string;
};

export type Connection = {
  host: string;
  port: number;
  database: string;
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
  /** user:<name>, token:<name> or webhook:<source>. */
  triggeredBy?: string;
  error?: string;
  createdAt: string;
  finishedAt?: string;
};

export type OpStatus = "running" | "succeeded" | "failed";

export type TargetKindName = "local" | "s3" | "gdrive" | "protondrive";

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
  /** A drive target's fields, secrets masked. */
  settings?: Record<string, string>;
  createdAt: string;
};

export type TargetInput = Omit<BackupTarget, "id" | "kind" | "createdAt" | "ageRecipient" | "settings"> & {
  kind?: TargetKindName;
  encrypt?: boolean;
  config?: Record<string, string>;
  /** A finished Proton sign-in, for a Proton Drive target. */
  login?: string;
};

/** A Proton sign-in: open url on any device, then wait for done. */
export type ProtonLogin = { id: string; url: string; status: "pending" | "done" | "failed"; error?: string };

/** A kind of target that can be added; drives need rclone on the manager. */
export type TargetKind = {
  kind: TargetKindName;
  label: string;
  description: string;
  available: boolean;
  /** How to get the credentials; `code` spans are commands. */
  help?: string;
  /** Credentials come from a browser sign-in (ProtonLogin), not fields. */
  signIn?: boolean;
  fields: {
    key: string;
    label: string;
    placeholder?: string;
    description?: string;
    required: boolean;
    secret: boolean;
    multiline?: boolean;
  }[];
};

export type Backup = {
  id: string;
  kind: "postgres" | "manager";
  encrypted: boolean;
  serviceId: string;
  projectId: string;
  serviceName: string;
  projectName: string;
  targetId: string;
  objectKey: string;
  status: OpStatus;
  sizeBytes: number;
  sha256: string;
  pgVersion: string;
  durationMs: number;
  error?: string;
  createdAt: string;
  finishedAt?: string;
  verifyStatus?: OpStatus;
  verifyError?: string;
  verifyDetails: { tables: number; rows: number; dbBytes: number; durationMs: number };
  verifiedAt?: string;
};

export type ScheduleInput = {
  targetId: string;
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
  serviceId: string;
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

export type Status = {
  version: string;
  update: UpdateInfo;
  docker?: { version: string; apiVersion: string; os: string; arch: string };
  dockerError?: string;
};

/** Secret values come back masked; sending the mask keeps the stored value. */
export const SECRET_MASK = "********";

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

export const api = {
  authState: () => request<AuthState>("/auth/state"),
  setup: (setupToken: string, username: string, password: string) =>
    request<User>("/auth/setup", json("POST", { setupToken, username, password })),
  login: (username: string, password: string) => request<User>("/auth/login", json("POST", { username, password })),
  logout: () => request<void>("/auth/logout", { method: "POST" }),

  status: () => request<Status>("/status"),
  applyUpdate: () => request<UpdateInfo>("/update", { method: "POST" }),
  checkUpdate: () => request<UpdateInfo>("/update/check", { method: "POST" }),

  projects: () => request<Project[]>("/projects"),
  project: (id: string) => request<Project>(`/projects/${id}`),
  topology: (projectId?: string) => request<Topology>(projectId ? `/projects/${projectId}/topology` : "/topology"),
  createProject: (name: string, serverId = "") => request<Project>("/projects", json("POST", { name, serverId })),
  setProjectEnv: (id: string, env: Record<string, string>, secrets: string[]) =>
    request<Project>(`/projects/${id}/env`, json("PUT", { env, secrets })),
  deleteProject: (id: string, confirm: string) =>
    request<void>(`/projects/${id}?confirm=${encodeURIComponent(confirm)}`, { method: "DELETE" }),

  services: (projectId: string) => request<Service[]>(`/projects/${projectId}/services`),
  service: (id: string) => request<Service>(`/services/${id}`),
  createService: (projectId: string, input: ServiceInput) =>
    request<Service>(`/projects/${projectId}/services`, json("POST", input)),
  updateService: (id: string, patch: ServicePatch) => request<Service>(`/services/${id}`, json("PATCH", patch)),
  deleteService: (id: string, confirm = "") =>
    request<void>(`/services/${id}?confirm=${encodeURIComponent(confirm)}`, { method: "DELETE" }),
  webhook: (id: string) => request<{ url: string; secret: string }>(`/services/${id}/webhook`),
  connection: (id: string) => request<Connection>(`/services/${id}/connection`),
  serviceAction: (id: string, action: "start" | "stop" | "restart") =>
    request<Service>(`/services/${id}/${action}`, { method: "POST" }),

  rollback: (serviceId: string, deploymentId = "") =>
    request<Deployment>(`/services/${serviceId}/rollback`, json("POST", { deploymentId })),
  deploy: (serviceId: string) => request<Deployment>(`/services/${serviceId}/deploy`, { method: "POST" }),
  deployments: (serviceId: string) => request<Deployment[]>(`/services/${serviceId}/deployments`),
  deploymentLog: (id: string) => request<string>(`/deployments/${id}/log`),

  backupTargetKinds: () => request<TargetKind[]>("/backup-target-kinds"),
  startProtonLogin: () => request<ProtonLogin>("/proton-logins", { method: "POST" }),
  protonLogin: (id: string) => request<ProtonLogin>(`/proton-logins/${id}`),
  backupTargets: () => request<BackupTarget[]>("/backup-targets"),
  createBackupTarget: (t: TargetInput) => request<BackupTarget>("/backup-targets", json("POST", t)),
  updateBackupTarget: (id: string, t: TargetInput) => request<BackupTarget>(`/backup-targets/${id}`, json("PUT", t)),
  backupTargetKey: (id: string) => request<{ identity: string; recipient: string }>(`/backup-targets/${id}/key`),
  backupManager: (targetId: string) => request<Backup>("/manager/backups", json("POST", { targetId })),
  deleteBackupTarget: (id: string) => request<void>(`/backup-targets/${id}`, { method: "DELETE" }),

  backups: (projectId?: string) => request<Backup[]>(`/backups${projectId ? `?projectId=${projectId}` : ""}`),
  serviceBackups: (serviceId: string) => request<Backup[]>(`/services/${serviceId}/backups`),
  backup: (serviceId: string, targetId: string) =>
    request<Backup>(`/services/${serviceId}/backups`, json("POST", { targetId })),
  getBackup: (id: string) => request<Backup>(`/backups/${id}`),
  verifyBackup: (id: string) => request<Backup>(`/backups/${id}/verify`, { method: "POST" }),
  deleteBackup: (id: string) => request<void>(`/backups/${id}`, { method: "DELETE" }),
  downloadUrl: (id: string) => `/api/backups/${id}/download`,
  restore: (backupId: string, confirm: string, serviceId = "") =>
    request<Restore>(`/backups/${backupId}/restore`, json("POST", { serviceId, confirm })),
  schedules: (serviceId: string) => request<Schedule[]>(`/services/${serviceId}/schedules`),
  createSchedule: (serviceId: string, s: ScheduleInput) =>
    request<Schedule>(`/services/${serviceId}/schedules`, json("POST", s)),
  updateSchedule: (id: string, s: ScheduleInput) => request<Schedule>(`/schedules/${id}`, json("PUT", s)),
  deleteSchedule: (id: string) => request<void>(`/schedules/${id}`, { method: "DELETE" }),
  backupProject: (projectId: string, targetId = "") =>
    request<{ backups: Backup[]; error?: string }>(`/projects/${projectId}/backups`, json("POST", { targetId })),
  restores: (serviceId: string) => request<Restore[]>(`/services/${serviceId}/restores`),

  servers: () => request<Server[]>("/servers"),
  createServer: (s: ServerInput) => request<Server>("/servers", json("POST", s)),
  updateServer: (id: string, s: ServerInput) => request<Server>(`/servers/${id}`, json("PUT", s)),
  deleteServer: (id: string) => request<void>(`/servers/${id}`, { method: "DELETE" }),
  registries: () => request<Registry[]>("/registries"),
  createRegistry: (r: RegistryInput) => request<Registry>("/registries", json("POST", r)),
  updateRegistry: (id: string, r: RegistryInput) => request<Registry>(`/registries/${id}`, json("PUT", r)),
  deleteRegistry: (id: string) => request<void>(`/registries/${id}`, { method: "DELETE" }),
  sshKey: () => request<{ publicKey: string }>("/ssh-key"),
  domains: () => request<Domain[]>("/domains"),
  createDomain: (name: string) => request<Domain>("/domains", json("POST", { name })),
  deleteDomain: (id: string) => request<void>(`/domains/${id}`, { method: "DELETE" }),
  setDomainProxied: (id: string, proxied: boolean) => request<Domain>(`/domains/${id}`, json("PATCH", { proxied })),
  cloudflare: () => request<Cloudflare>("/cloudflare"),
  connectCloudflare: (token: string) => request<Cloudflare>("/cloudflare", json("PUT", { token })),
  disconnectCloudflare: () => request<void>("/cloudflare", { method: "DELETE" }),
  secretProviders: () => request<SecretProvider[]>("/secret-providers"),
  connectSecretProvider: (id: string, token: string) =>
    request<SecretProvider>(`/secret-providers/${id}`, json("PUT", { token })),
  secretVaults: (id: string) => request<string[]>(`/secret-providers/${id}/vaults`),
  secretItems: (id: string, vault: string) =>
    request<SecretItem[]>(`/secret-providers/${id}/items?vault=${encodeURIComponent(vault)}`),
  disconnectSecretProvider: (id: string) => request<void>(`/secret-providers/${id}`, { method: "DELETE" }),
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
};
