import { createPasskey, getPasskey, type CreationOptionsJSON, type RequestOptionsJSON } from "./lib/webauthn";
import type { AnalyticsSettings, AnalyticsStats, AnalyticsStep, AnalyticsVisit, AnalyticsVisitDetail, DeployErrors, ErrorsView, Issue, IssueEvent, IssueStatus, SharedAnalytics, Tracker, TrackerInput, TrackerStats } from "@/features/analytics/types";
import type { Account, ApiToken, AuditEntry, AuthState, Ceremony, Invite, InviteInfo, InviteInput, MfaChallenge, Passkey, Preferences, PushDevice, SSOProvider, SSOProviderInput, TokenInput, TotpSetup, User, UserAccess, UserView } from "@/features/auth/types";
import type { Backup, BackupTarget, Restore, ScanResult, Schedule, ScheduleInput, TargetInput } from "@/features/backups/types";
import type { ChangeResult, ConsoleResult, DocChanges, DocQuery, KeyChanges, MongoDocs, NewColumn, PgDatabase, PgRows, PgTable, RedisKeys, RedisValue, RowChanges, RowQuery } from "@/features/data/types";
import type { VolumeEntry, VolumeFile, VolumeListing } from "@/features/files/types";
import type { Topology } from "@/features/map/types";
import type { AnonymizeRules, AppTemplate, CanvasPoint, ComposePlan, GitInput, GitStatus, PreviewSettings, PreviewView, Project, SeedDatabase, TagInfo, TagList, TemplateInstall, TemplateLogo, TemplateResult } from "@/features/projects/types";
import type { Connection, Deployment, Job, JobInput, JobRun, Maintenance, Service, ServiceInput, ServicePatch, ServiceStats, ServiceUsage, Status, Uptime, UptimeInput } from "@/features/services/types";
import type { Branding, BrandingInput, ChannelInput, Cleanup, CleanupSettings, Cloudflare, Domain, Email, EmailProvider, EmailSettings, GitProvider, GitProviderInput, GitRepo, Network, NotificationChannel, Notifications, Registry, RegistryInput, ResourceAlerts, SecretConnection, SecretItem, SecretProvider, SecretStep, SecurityReport, Server, ServerInput, ServerNetwork, ServerStats, UpdateInfo, UpdatePreflight } from "@/features/settings/types";
import { ApiError, json, progress, request, test } from "@/lib/api";

export * from "@/features/services/types";
export * from "@/features/projects/types";
export * from "@/features/map/types";
export * from "@/features/backups/types";
export * from "@/features/auth/types";
export * from "@/features/settings/types";
export * from "@/features/analytics/types";
export * from "@/features/data/types";
export * from "@/features/files/types";
export { ApiError } from "@/lib/api";

/**
 * Uploads a file into a service's volumes with progress (fetch can't report
 * it). Missing folders are created; an existing file is a 409 unless overwrite.
 */
function uploadVolumeFile(
  serviceId: string,
  path: string,
  body: Blob,
  opts: { overwrite?: boolean; onProgress?: (sent: number) => void; signal?: AbortSignal } = {},
): Promise<VolumeEntry> {
  // The demo answers fetch only.
  if (import.meta.env.MODE === "demo")
    return request<VolumeEntry>(`/services/${serviceId}/files/content?${new URLSearchParams({ path, ...(opts.overwrite ? { overwrite: "true" } : {}) })}`, {
      method: "PUT",
      body,
      signal: opts.signal,
    });
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    const q = new URLSearchParams({ path, ...(opts.overwrite ? { overwrite: "true" } : {}) });
    xhr.open("PUT", `/api/services/${serviceId}/files/content?${q}`);
    xhr.setRequestHeader("Content-Type", "application/octet-stream");
    xhr.upload.onprogress = (e) => opts.onProgress?.(e.loaded);
    xhr.onload = () => {
      let body: { error?: string } & Partial<VolumeEntry> = {};
      try {
        body = JSON.parse(xhr.responseText);
      } catch {
        // not JSON: keep the status text
      }
      if (xhr.status >= 200 && xhr.status < 300) resolve(body as VolumeEntry);
      else reject(new ApiError(xhr.status, body.error ?? xhr.statusText));
    };
    xhr.onerror = () => reject(new TypeError("network error"));
    xhr.onabort = () => reject(new DOMException("upload cancelled", "AbortError"));
    opts.signal?.addEventListener("abort", () => xhr.abort());
    xhr.send(body);
  });
}

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
  /** The provider's sign-in page; it sends the browser back to returnTo. */
  beginSSO: (id: string, returnTo: string) => request<{ url: string }>(`/auth/sso/${id}/begin`, json("POST", { returnTo })),
  logout: () => request<void>("/auth/logout", { method: "POST" }),
  invite: (token: string) => request<InviteInfo>("/auth/invite", json("POST", { token })),
  /** The provider's sign-in page, to create the invited account signing in with it. */
  beginSSOInvite: (id: string, token: string, username: string) =>
    request<{ url: string }>(`/auth/sso/${id}/invite`, json("POST", { token, username })),
  acceptInvite: (token: string, username: string, password: string) =>
    request<User>("/auth/invite/accept", json("POST", { token, username, password })),

  account: () => request<Account>("/account"),
  setPreferences: (p: Preferences) => request<Preferences>("/account/preferences", json("PUT", p)),
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
  /** publicKey: the applicationServerKey browsers subscribe with. */
  pushDevices: () => request<{ publicKey: string; devices: PushDevice[] }>("/account/push"),
  /** sub: the browser's PushSubscription.toJSON(). */
  subscribePush: (name: string, { endpoint, keys }: PushSubscriptionJSON) =>
    request<PushDevice>("/account/push", json("POST", { name, endpoint, keys })),
  unsubscribePush: (id: string) => request<void>(`/account/push/${id}`, { method: "DELETE" }),
  /** The provider's sign-in page, to link the identity signed in there. */
  connectSSO: (id: string, password: string) => request<{ url: string }>(`/account/sso/${id}/connect`, json("POST", { password })),
  unlinkSSO: (id: string) => request<void>(`/account/sso/${id}`, { method: "DELETE" }),
  /** The provider's sign-in page, asking for the credentials again to confirm a change; back to returnTo. */
  confirmSSO: (id: string, returnTo: string) => request<{ url: string }>(`/account/sso/${id}/confirm`, json("POST", { returnTo })),

  status: () => request<Status>("/status"),
  applyUpdate: () => request<UpdateInfo>("/update", { method: "POST" }),
  checkUpdate: () => request<UpdateInfo>("/update/check", { method: "POST" }),
  updatePreflight: () => request<UpdatePreflight>("/update/preflight"),

  projects: () => request<Project[]>("/projects"),
  project: (id: string) => request<Project>(`/projects/${id}`),
  topology: (projectId?: string) => request<Topology>(projectId ? `/projects/${projectId}/topology` : "/topology"),
  createProject: (name: string, serverId = "") => request<Project>("/projects", json("POST", { name, serverId })),
  setProjectEnv: (id: string, env: Record<string, string>, secrets: string[]) =>
    request<Project>(`/projects/${id}/env`, json("PUT", { env, secrets })),
  previews: (id: string) => request<PreviewView[]>(`/projects/${id}/previews`),
  closePreview: (id: string, name: string) =>
    request<void>(`/projects/${id}/previews/${encodeURIComponent(name)}`, { method: "DELETE" }),
  previewSettings: (id: string) => request<PreviewSettings>(`/projects/${id}/preview-settings`),
  setPreviewSettings: (id: string, s: PreviewSettings) => request<PreviewSettings>(`/projects/${id}/preview-settings`, json("PUT", s)),
  compose: (id: string, service?: string) => request<string>(composePath(id, service)),
  composeUrl: (id: string, service?: string) => `/api${composePath(id, service)}${service ? "&" : "?"}download=1`,
  applyCompose: (id: string, body: { compose: string; env: string; prune: boolean; dryRun?: boolean; deploy?: boolean }) =>
    request<ComposePlan>(`/projects/${id}/compose`, json("POST", body)),
  templates: () => request<AppTemplate[]>("/templates"),
  templateLogos: () => request<TemplateLogo[]>("/templates/logos"),
  installTemplate: (id: string, body: TemplateInstall) => request<TemplateResult>(`/templates/${id}/install`, json("POST", body)),
  /** The project's git link; null when it has none. */
  previewSeed: (id: string) => request<SeedDatabase[]>(`/projects/${id}/preview-seed`),
  /** rules null removes them. */
  setAnonymizeRules: (id: string, service: string, rules: AnonymizeRules | null) =>
    request<SeedDatabase>(`/projects/${id}/preview-seed/${encodeURIComponent(service)}`, json("PUT", { rules })),
  anonymizeNow: (id: string, service: string) =>
    request<Backup>(`/projects/${id}/preview-seed/${encodeURIComponent(service)}/anonymize`, { method: "POST" }),
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
  setProjectTags: (id: string, tags: string[]) => request<Project>(`/projects/${id}/tags`, json("PUT", { tags })),
  tags: () => request<TagList>("/tags"),
  /** An empty color goes back to the one derived from the name. */
  setTagColor: (name: string, color: string) => request<TagInfo>(`/tags/${encodeURIComponent(name)}`, json("PUT", { color })),
  setProjectSleep: (id: string, sleepAfterSeconds: number) =>
    request<Project>(`/projects/${id}/sleep`, json("PUT", { sleepAfterSeconds })),
  deleteProject: (id: string, confirm: string) =>
    request<void>(`/projects/${id}?confirm=${encodeURIComponent(confirm)}`, { method: "DELETE" }),

  services: (projectId: string) => request<Service[]>(`/projects/${projectId}/services`),
  service: (id: string) => request<Service>(`/services/${id}`),
  createService: (projectId: string, input: ServiceInput) =>
    request<Service>(`/projects/${projectId}/services`, json("POST", input)),
  updateService: (id: string, patch: ServicePatch) => request<Service>(`/services/${id}`, json("PATCH", patch)),
  renameService: (id: string, name: string) => request<Service>(`/services/${id}/name`, json("PUT", { name })),
  setServiceNetworks: (id: string, networks: string[]) => request<Service>(`/services/${id}/networks`, json("PUT", { networks })),
  setServiceTags: (id: string, tags: string[]) => request<Service>(`/services/${id}/tags`, json("PUT", { tags })),
  setMaintenance: (id: string, m: Required<Maintenance>) => request<Project>(`/projects/${id}/maintenance`, json("PUT", m)),
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
  encryptBackupTarget: (id: string, ageIdentity = "") =>
    request<BackupTarget>(`/storage/${id}/encrypt`, json("POST", { ageIdentity })),
  scanBackupTarget: (id: string) => request<ScanResult>(`/storage/${id}/scan`, { method: "POST" }),
  testBackupTarget: (id: string, onStep?: (step: string) => void) => test(`/storage/${id}/test`, onStep),

  backups: (projectId?: string) => request<Backup[]>(`/backups${projectId ? `?projectId=${projectId}` : ""}`),
  serviceBackups: (serviceId: string) => request<Backup[]>(`/services/${serviceId}/backups`),
  backup: (serviceId: string, targetId: string, database = "") =>
    request<Backup>(`/services/${serviceId}/backups`, json("POST", { targetId, database })),
  getBackup: (id: string) => request<Backup>(`/backups/${id}`),
  verifyBackup: (id: string) => request<Backup>(`/backups/${id}/verify`, { method: "POST" }),
  deleteBackup: (id: string) => request<void>(`/backups/${id}`, { method: "DELETE" }),
  downloadUrl: (id: string) => `/api/backups/${id}/download`,
  pgDatabases: (serviceId: string) => request<PgDatabase[]>(`/services/${serviceId}/data/databases`),
  /** collection: a MongoDB database's first one, which makes it exist. */
  createPgDatabase: (serviceId: string, name: string, collection = "") =>
    request<PgDatabase>(`/services/${serviceId}/data/databases`, json("POST", { name, collection })),
  pgTables: (serviceId: string, database: string) =>
    request<PgTable[]>(`/services/${serviceId}/data/tables?${new URLSearchParams({ database })}`),
  pgRows: (serviceId: string, database: string, schema: string, table: string, q: RowQuery) =>
    request<PgRows>(`/services/${serviceId}/data/tables/${encodeURIComponent(schema)}/${encodeURIComponent(table)}?${rowParams(q, database)}`),
  pgExportUrl: (serviceId: string, database: string, schema: string, table: string, q: RowQuery) =>
    `/api/services/${serviceId}/data/tables/${encodeURIComponent(schema)}/${encodeURIComponent(table)}/export?${rowParams({ ...q, limit: 0, offset: 0 }, database)}`,
  /** Without apply, only returns the statements the save would run. */
  rowChanges: (serviceId: string, database: string, schema: string, table: string, changes: RowChanges, apply: boolean) =>
    request<ChangeResult>(
      `/services/${serviceId}/data/tables/${encodeURIComponent(schema)}/${encodeURIComponent(table)}/changes`,
      json("POST", { database, changes, apply }),
    ),
  docChanges: (serviceId: string, database: string, collection: string, changes: DocChanges, apply: boolean) =>
    request<ChangeResult>(`/services/${serviceId}/data/collections/${encodeURIComponent(collection)}/changes`, json("POST", { database, changes, apply })),
  keyChanges: (serviceId: string, key: string, changes: KeyChanges, apply: boolean) =>
    request<ChangeResult>(`/services/${serviceId}/data/key/changes`, json("POST", { key, changes, apply })),
  /** Without apply, these only return the statement they would run. */
  createTable: (serviceId: string, database: string, schema: string, name: string, columns: NewColumn[], apply: boolean) =>
    request<ChangeResult>(`/services/${serviceId}/data/tables`, json("POST", { database, schema, name, columns, apply })),
  dropTable: (serviceId: string, database: string, schema: string, table: string, apply: boolean) =>
    request<ChangeResult>(`/services/${serviceId}/data/tables/${encodeURIComponent(schema)}/${encodeURIComponent(table)}/drop`, json("POST", { database, apply })),
  createCollection: (serviceId: string, database: string, name: string, apply: boolean) =>
    request<ChangeResult>(`/services/${serviceId}/data/collections`, json("POST", { database, name, apply })),
  dropCollection: (serviceId: string, database: string, collection: string, apply: boolean) =>
    request<ChangeResult>(`/services/${serviceId}/data/collections/${encodeURIComponent(collection)}/drop`, json("POST", { database, apply })),
  redisScan: (serviceId: string, cursor: string, pattern: string) =>
    request<RedisKeys>(`/services/${serviceId}/data/keys?${new URLSearchParams({ cursor, pattern })}`),
  redisGet: (serviceId: string, key: string, cursor = "0") =>
    request<RedisValue>(`/services/${serviceId}/data/key?${new URLSearchParams({ key, cursor })}`),
  mongoDocuments: (serviceId: string, database: string, collection: string, q: DocQuery) =>
    request<MongoDocs>(
      `/services/${serviceId}/data/collections/${encodeURIComponent(collection)}/documents?${new URLSearchParams({
        database,
        filter: q.filter ?? "",
        sort: q.sort ?? "",
        limit: String(q.limit ?? 0),
        skip: String(q.skip ?? 0),
      })}`,
    ),
  dataConsole: (serviceId: string, query: string, write: boolean, database = "") =>
    request<ConsoleResult>(`/services/${serviceId}/data/console`, json("POST", { query, write, database })),
  volumeFiles: (serviceId: string, path: string) =>
    request<VolumeListing>(`/services/${serviceId}/files?${new URLSearchParams({ path })}`),
  volumeFile: (serviceId: string, path: string) =>
    request<VolumeFile>(`/services/${serviceId}/files/content?${new URLSearchParams({ path })}`),
  /** A file as is; a folder, or names in folder path, as a .tar.gz. */
  volumeDownloadUrl: (serviceId: string, path: string, names: string[] = []) =>
    `/api/services/${serviceId}/files/download?${new URLSearchParams([["path", path], ...names.map((n) => ["name", n])])}`,
  uploadVolumeFile,
  /** Saves an edited text file; modified is the time it was read, so a file changed since is a 409. */
  saveVolumeFile: (serviceId: string, path: string, content: string, modified?: string) =>
    request<VolumeEntry>(`/services/${serviceId}/files/content?${new URLSearchParams({ path, overwrite: "true", ...(modified ? { modified } : {}) })}`, {
      method: "PUT",
      body: content,
      headers: { "Content-Type": "text/plain; charset=utf-8" },
    }),
  makeVolumeDir: (serviceId: string, path: string) => request<void>(`/services/${serviceId}/files/mkdir`, json("POST", { path })),
  moveVolumePath: (serviceId: string, from: string, to: string) =>
    request<void>(`/services/${serviceId}/files/move`, json("POST", { from, to })),
  copyVolumePath: (serviceId: string, from: string, to: string) =>
    request<void>(`/services/${serviceId}/files/copy`, json("POST", { from, to })),
  deleteVolumePaths: (serviceId: string, paths: string[]) =>
    request<void>(`/services/${serviceId}/files/delete`, json("POST", { paths })),
  restore: (backupId: string, confirm: string, serviceId = "") =>
    request<Restore>(`/backups/${backupId}/restore`, json("POST", { serviceId, confirm })),
  schedules: (serviceId: string) => request<Schedule[]>(`/services/${serviceId}/schedules`),
  createSchedule: (serviceId: string, s: ScheduleInput) =>
    request<Schedule>(`/services/${serviceId}/schedules`, json("POST", s)),
  managerSchedules: () => request<Schedule[]>("/manager/schedules"),
  createManagerSchedule: (s: ScheduleInput) => request<Schedule>("/manager/schedules", json("POST", s)),
  updateSchedule: (id: string, s: ScheduleInput) => request<Schedule>(`/schedules/${id}`, json("PUT", s)),
  deleteSchedule: (id: string) => request<void>(`/schedules/${id}`, { method: "DELETE" }),
  jobs: (serviceId: string) => request<Job[]>(`/services/${serviceId}/jobs`),
  createJob: (serviceId: string, j: JobInput) => request<Job>(`/services/${serviceId}/jobs`, json("POST", j)),
  updateJob: (id: string, j: JobInput) => request<Job>(`/jobs/${id}`, json("PUT", j)),
  deleteJob: (id: string) => request<void>(`/jobs/${id}`, { method: "DELETE" }),
  runJob: (id: string) => request<JobRun>(`/jobs/${id}/run`, { method: "POST" }),
  jobRuns: (id: string) => request<JobRun[]>(`/jobs/${id}/runs`),
  jobRunLog: (id: string, runId: string) => request<string>(`/jobs/${id}/runs/${runId}/log`),
  backupProject: (projectId: string, targetId = "") =>
    request<{ backups: Backup[]; error?: string }>(`/projects/${projectId}/backups`, json("POST", { targetId })),
  restores: (serviceId: string) => request<Restore[]>(`/services/${serviceId}/restores`),

  servers: () => request<Server[]>("/servers"),
  serverStats: (id: string) => request<ServerStats>(`/servers/${id}/stats`),
  serverSecurity: (id: string) => request<SecurityReport>(`/servers/${id}/security`),
  checkServerSecurity: (id: string) => request<SecurityReport>(`/servers/${id}/security/check`, { method: "POST" }),
  installServerUpdates: (id: string, all: boolean) =>
    request<SecurityReport>(`/servers/${id}/security/updates`, json("POST", { all })),
  rebootServer: (id: string) => request<SecurityReport>(`/servers/${id}/reboot`, { method: "POST" }),
  /** With onStep, reports each step of reaching and preparing the server as it starts. */
  createServer: (s: ServerInput, onStep?: (step: string) => void) =>
    onStep ? progress<Server>("/servers", json("POST", s), onStep) : request<Server>("/servers", json("POST", s)),
  updateServer: (id: string, s: ServerInput, onStep?: (step: string) => void) =>
    onStep ? progress<Server>(`/servers/${id}`, json("PUT", s), onStep) : request<Server>(`/servers/${id}`, json("PUT", s)),
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
  testGitProvider: (id: string, onStep?: (step: string) => void) => test(`/git-providers/${id}/test`, onStep),
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
  testRegistry: (id: string, onStep?: (step: string) => void) => test(`/registries/${id}/test`, onStep),
  ssoProviders: () => request<SSOProvider[]>("/sso-providers"),
  createSSOProvider: (p: SSOProviderInput) => request<SSOProvider>("/sso-providers", json("POST", p)),
  updateSSOProvider: (id: string, p: SSOProviderInput) => request<SSOProvider>(`/sso-providers/${id}`, json("PUT", p)),
  deleteSSOProvider: (id: string) => request<void>(`/sso-providers/${id}`, { method: "DELETE" }),
  ssoRequired: () => request<{ required: boolean }>("/sso-required"),
  setSSORequired: (required: boolean) => request<{ required: boolean }>("/sso-required", json("PUT", { required })),
  sshKey: () => request<{ publicKey: string }>("/ssh-key"),
  domains: () => request<Domain[]>("/domains"),
  createDomain: (name: string) => request<Domain>("/domains", json("POST", { name })),
  deleteDomain: (id: string) => request<void>(`/domains/${id}`, { method: "DELETE" }),
  setDomainProxied: (id: string, proxied: boolean) => request<Domain>(`/domains/${id}`, json("PATCH", { proxied })),
  cloudflare: () => request<Cloudflare>("/cloudflare"),
  connectCloudflare: (token: string) => request<Cloudflare>("/cloudflare", json("PUT", { token })),
  disconnectCloudflare: () => request<void>("/cloudflare", { method: "DELETE" }),
  testCloudflare: (onStep?: (step: string) => void) => test("/cloudflare/test", onStep),
  syncCloudflare: () => request<Cloudflare>("/cloudflare/sync", { method: "POST" }),
  secretProviders: () => request<SecretProvider[]>("/secret-providers"),
  secretConnections: () => request<SecretConnection[]>("/secret-connections"),
  /** name is "" for the provider's default connection. */
  createSecretConnection: (kind: string, name: string, token: string, onStep?: (step: SecretStep) => void) =>
    onStep
      ? progress<SecretConnection>("/secret-connections", json("POST", { kind, name, token }), onStep as (s: string) => void)
      : request<SecretConnection>("/secret-connections", json("POST", { kind, name, token })),
  /** Listings are cached by the manager for a few minutes, unless refresh. */
  secretVaults: (id: string, refresh = false) =>
    request<string[]>(`/secret-connections/${id}/vaults${refresh ? "?refresh" : ""}`),
  secretItems: (id: string, vault: string, refresh = false) =>
    request<SecretItem[]>(`/secret-connections/${id}/items?vault=${encodeURIComponent(vault)}${refresh ? "&refresh" : ""}`),
  deleteSecretConnection: (id: string) => request<void>(`/secret-connections/${id}`, { method: "DELETE" }),
  testSecretConnection: (id: string, onStep?: (step: SecretStep) => void) =>
    test(`/secret-connections/${id}/test`, onStep as ((s: string) => void) | undefined),
  setServerNetwork: (id: string, n: ServerNetwork) => request<Server>(`/servers/${id}/network`, json("PUT", n)),
  setBranding: (b: BrandingInput) => request<Branding>("/branding", json("PUT", b)),
  resetBranding: () => request<Branding>("/branding", { method: "DELETE" }),
  analytics: () => request<AnalyticsSettings>("/analytics"),
  setAnalytics: (s: AnalyticsSettings, onStep?: (step: AnalyticsStep) => void) =>
    onStep
      ? progress<AnalyticsSettings>("/analytics", json("PUT", s), (step) => onStep(step as AnalyticsStep))
      : request<AnalyticsSettings>("/analytics", json("PUT", s)),
  deleteAnalyticsData: () => request<void>("/analytics/data", { method: "DELETE" }),
  /** filters are "<breakdown>:<value>". */
  serviceAnalytics: (id: string, { filters = [], ...q }: { from: number; to: number; interval: number; offset: number; filters?: string[] }) => {
    const p = new URLSearchParams(Object.entries(q).map(([k, v]) => [k, String(v)]));
    for (const f of filters) p.append("filter", f);
    return request<AnalyticsStats>(`/services/${id}/analytics?${p}`);
  },
  serviceVisits: (id: string, { filters = [], ...q }: { from: number; to: number; before: string; limit: number; filters?: string[] }) => {
    const p = new URLSearchParams(Object.entries(q).map(([k, v]) => [k, String(v)]));
    for (const f of filters) p.append("filter", f);
    return request<AnalyticsVisit[]>(`/services/${id}/analytics/visits?${p}`);
  },
  /** A CSV of the range: its summary, series, breakdowns or visits. */
  analyticsExportUrl: (id: string, table: string, { filters = [], ...q }: { from: number; to: number; interval: number; offset: number; filters?: string[] }) => {
    const p = new URLSearchParams(Object.entries({ ...q, table }).map(([k, v]) => [k, String(v)]));
    for (const f of filters) p.append("filter", f);
    return `/api/services/${id}/analytics/export?${p}`;
  },
  /** A new public link to the app's analytics (revoking the one before); the token is only returned now. */
  shareAnalytics: (id: string) => request<{ token: string }>(`/services/${id}/analytics/share`, { method: "POST" }),
  unshareAnalytics: (id: string) => request<void>(`/services/${id}/analytics/share`, { method: "DELETE" }),
  /** A shared link's stats, without a session: no identities, properties or visits. */
  sharedAnalytics: (token: string, { filters: _, ...q }: { from: number; to: number; interval: number; offset: number; filters?: string[] }) => {
    const p = new URLSearchParams(Object.entries(q).map(([k, v]) => [k, String(v)]));
    return request<SharedAnalytics>(`/share/analytics/${encodeURIComponent(token)}?${p}`);
  },
  /** Visits of the range through steps in order, "page:<path>" or "event:<name>". */
  serviceFunnel: (id: string, { steps, ...q }: { from: number; to: number; steps: string[] }) => {
    const p = new URLSearchParams(Object.entries(q).map(([k, v]) => [k, String(v)]));
    for (const s of steps) p.append("step", s);
    return request<{ step: string; visits: number }[]>(`/services/${id}/analytics/funnel?${p}`);
  },
  serviceErrors: (id: string, q: { status?: IssueStatus; q?: string; sort?: string; offset?: number }) => {
    const p = new URLSearchParams(Object.entries(q).flatMap(([k, v]) => (v === undefined || v === "" ? [] : [[k, String(v)]])));
    return request<ErrorsView>(`/services/${id}/errors?${p}`);
  },
  serviceErrorSeries: (id: string, q: { from: number; to: number; interval: number }) =>
    request<{ t: number; n: number }[]>(`/services/${id}/errors/series?${new URLSearchParams(Object.entries(q).map(([k, v]) => [k, String(v)]))}`),
  serviceDeployErrors: (id: string) => request<DeployErrors>(`/services/${id}/errors/deploy`),
  serviceIssue: (id: string, issue: number) => request<Issue>(`/services/${id}/errors/${issue}`),
  serviceIssueEvent: (id: string, issue: number, event = "") =>
    request<IssueEvent>(`/services/${id}/errors/${issue}/event?event=${encodeURIComponent(event)}`),
  setIssueStatus: (id: string, issue: number, status: IssueStatus) =>
    request<Issue>(`/services/${id}/errors/${issue}/status`, json("PUT", { status })),
  trackers: () => request<Tracker[]>("/analytics/trackers"),
  createTracker: (t: TrackerInput) => request<Tracker>("/analytics/trackers", json("POST", t)),
  updateTracker: (id: string, t: TrackerInput) => request<Tracker>(`/analytics/trackers/${id}`, json("PUT", t)),
  deleteTracker: (id: string) => request<void>(`/analytics/trackers/${id}`, { method: "DELETE" }),
  trackerAnalytics: (id: string, { filters = [], ...q }: { from: number; to: number; interval: number; offset: number; filters?: string[] }) => {
    const p = new URLSearchParams(Object.entries(q).map(([k, v]) => [k, String(v)]));
    for (const f of filters) p.append("filter", f);
    return request<TrackerStats>(`/analytics/trackers/${id}/stats?${p}`);
  },
  serviceVisit: (id: string, visit: string) => request<AnalyticsVisitDetail>(`/services/${id}/analytics/visits/${visit}`),
  cleanup: () => request<Cleanup>("/cleanup"),
  setCleanup: (s: CleanupSettings) => request<Cleanup>("/cleanup", json("PUT", s)),
  runCleanup: () => request<Cleanup>("/cleanup/run", { method: "POST" }),
  email: () => request<Email>("/email"),
  setEmailInvites: (provider: string) => request<Email>("/email/invites", json("PUT", { provider })),
  createEmailProvider: (s: EmailSettings) => request<EmailProvider>("/email/providers", json("POST", s)),
  updateEmailProvider: (id: string, s: EmailSettings) => request<EmailProvider>(`/email/providers/${id}`, json("PUT", s)),
  deleteEmailProvider: (id: string) => request<void>(`/email/providers/${id}`, { method: "DELETE" }),
  testEmailProvider: (id: string, to: string) => request<void>(`/email/providers/${id}/test`, json("POST", { to })),
  notifications: () => request<Notifications>("/notifications"),
  createChannel: (c: ChannelInput) => request<NotificationChannel>("/notifications/channels", json("POST", c)),
  updateChannel: (id: string, c: ChannelInput) =>
    request<NotificationChannel>(`/notifications/channels/${id}`, json("PUT", c)),
  deleteChannel: (id: string) => request<void>(`/notifications/channels/${id}`, { method: "DELETE" }),
  testChannel: (id: string) => request<void>(`/notifications/channels/${id}/test`, { method: "POST" }),
  setThresholds: (t: ResourceAlerts) => request<ResourceAlerts>("/notifications/thresholds", json("PUT", t)),
  tokens: () => request<ApiToken[]>("/tokens"),
  createToken: (t: TokenInput) => request<ApiToken & { token: string }>("/tokens", json("POST", t)),
  updateToken: (id: string, t: TokenInput) => request<ApiToken>(`/tokens/${id}`, json("PUT", t)),
  rollToken: (id: string) => request<ApiToken & { token: string }>(`/tokens/${id}/roll`, { method: "POST" }),
  deleteToken: (id: string) => request<void>(`/tokens/${id}`, { method: "DELETE" }),
  audit: () => request<AuditEntry[]>("/audit?limit=200"),
  users: () => request<UserView[]>("/users"),
  setUserAccess: (id: string, a: UserAccess) => request<UserView>(`/users/${id}`, json("PUT", a)),
  deleteUser: (id: string) => request<void>(`/users/${id}`, { method: "DELETE" }),
  invites: () => request<Invite[]>("/invites"),
  createInvite: (a: InviteInput) => request<Invite & { token: string; emailError?: string }>("/invites", json("POST", a)),
  deleteInvite: (id: string) => request<void>(`/invites/${id}`, { method: "DELETE" }),

  logsUrl: (serviceId: string, tail = 200) => `/api/services/${serviceId}/logs?tail=${tail}`,
  terminalUrl: (serviceId: string, container: string, shell: string) =>
    `/api/services/${serviceId}/terminal?${new URLSearchParams({ container, shell })}`,
  serverTerminalUrl: (serverId: string) => `/api/servers/${serverId}/terminal`,
};
