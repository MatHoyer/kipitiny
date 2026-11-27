import type { ServiceKind } from "@/features/services/types";

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

/** Credentials pulls from one registry host use; password reads back masked. */
export type Registry = { id: string; host: string; username: string; password: string; createdAt: string };

export type RegistryInput = Pick<Registry, "host" | "username" | "password">;

/** A service on a network created by hand, reached there as its alias. */
export type NetworkMember = { id: string; name: string; kind: ServiceKind; projectId: string; projectName: string; alias: string };

/** A network created by hand on a server, which services of any project there can join. */
export type Network = { id: string; serverId: string; name: string; dockerName: string; services: NetworkMember[]; createdAt: string };

/** The UI's look; absent fields keep kipitiny's. Colours are #rrggbb; accentDark replaces accent in dark mode. */
export type Theme = {
  accent?: string;
  accentDark?: string;
  tint?: string;
  radius?: "none" | "small" | "medium" | "large" | "full";
  font?: "geist" | "system" | "serif" | "mono";
  density?: "comfortable" | "compact";
};

/** The manager's name, logo URL and theme; absent ones keep kipitiny's. */
export type Branding = Theme & { name?: string; logo?: string };

/** logo: a data URL to set one, "" to remove it, absent to keep it. */
export type BrandingInput = Theme & { name: string; logo?: string };

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

export type EmailSecurity = "starttls" | "tls" | "none";

/** An SMTP server email goes through; the password comes back masked. */
export type EmailSettings = {
  name: string;
  host: string;
  port: number;
  security: EmailSecurity;
  username: string;
  password: string;
  /** The sender, with an optional name: "kipitiny <ops@example.com>". */
  from: string;
};

export type EmailProvider = EmailSettings & { id: string; createdAt: string };

/** invites: the ID of the provider invites go through, empty without providers. */
export type Email = { providers: EmailProvider[]; invites: string };

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
};

/** An account a password manager is logged into; a manager can have several. */
export type SecretConnection = {
  id: string;
  /** The SecretProvider's id. */
  kind: string;
  /** "" for the provider's default connection, referenced by its plain scheme. */
  name: string;
  /** What its references start with: pass for the default connection, pass+work for the one named work. */
  scheme: string;
  providerName: string;
  createdAt: string;
};

/** A stage of connecting or testing a password manager, streamed as it starts. */
export type SecretStep = "image" | "helper" | "login" | "verify" | "session" | "vaults";

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
  /** Where a running cleanup is at, on stepServer. */
  step?: "connect" | "containers" | "images" | "buildcache" | "volumes" | "networks" | "deployments";
  stepServer?: string;
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
  /** A field with options is a choice (an email channel's provider). */
  fields: { key: string; label: string; placeholder?: string; required: boolean; secret: boolean; options?: { value: string; label: string }[] }[];
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
  thresholds: ResourceAlerts;
};

/** When resource alerts fire: memory and CPU of their limit (the server's when unlimited), held for some minutes. */
export type ResourceAlerts = { memoryPercent: number; memoryMinutes: number; cpuPercent: number; cpuMinutes: number; diskFreePercent: number };

/** State of a service domain's managed Cloudflare record. */
export type DNSStatus = { state: "synced" | "warning" | "conflict" | "error"; message?: string };

/** Whether now is a good time to update the manager: a blocking check disables the update. */
export type UpdatePreflight = {
  checks: { id: "version" | "tasks" | "docker" | "backup"; state: "ok" | "warn" | "block"; detail: string }[];
  ready: boolean;
};

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
  /** What the versions up to latest do to running servers (UPGRADE.md), to read before updating. */
  notices?: UpgradeNotice[];
};

export type UpgradeNotice = { version: string; level: "info" | "warning"; text: string };

/** A server's host resource use at one moment. */
export type HostUsage = {
  at: string;
  /** Percent of the whole machine: 100 is every core busy. */
  cpu: number;
  cores: number;
  /** 1, 5 and 15 minute load average. */
  load: [number, number, number];
  memoryBytes: number;
  memoryTotalBytes: number;
  swapBytes: number;
  swapTotalBytes: number;
  /** The filesystem holding Docker's data. */
  diskBytes: number;
  diskTotalBytes: number;
  uptimeSec: number;
};

export type SecurityStatus = "ok" | "warn" | "fail" | "unknown";

/** One point of a host's security checklist. */
export type SecurityItem = {
  id: string;
  title: string;
  status: SecurityStatus;
  /** What was found. */
  detail?: string;
  /** What to do about it. */
  hint?: string;
};

/** The last check of a server's host; checkedAt is the zero time before the first. */
export type SecurityReport = {
  checkedAt: string;
  os?: string;
  items: SecurityItem[];
  /** Set when the host couldn't be checked at all. */
  error?: string;
  reboot: boolean;
  /** Pending package updates, security ones first. */
  updates: PendingUpdate[];
  /** The last update install started from kipitiny. */
  install?: UpdateRun;
  /** Set while a reboot started from kipitiny isn't over. */
  rebootingSince?: string;
};

/** reboot: takes effect after a reboot; docker: restarts Docker, so every app; ssh: restarts the SSH server. */
export type UpdateImpact = "reboot" | "docker" | "ssh";

export type PendingUpdate = { name: string; version?: string; security: boolean; impact?: UpdateImpact };

export type UpdateRun = {
  /** Every pending update, not only security ones. */
  all: boolean;
  startedAt: string;
  finishedAt?: string;
  running: boolean;
  error?: string;
  log?: string;
};

export type ServerStats = {
  /** Null while the server can't be read. */
  current: HostUsage | null;
  /** The last five minutes, one point every ten seconds. */
  history: HostUsage[];
};

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
