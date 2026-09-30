export type Project = {
  id: string;
  name: string;
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
  memoryMb: number;
  databaseId: string;
  createdAt: string;
  updatedAt: string;
  containers: Container[];
};

export type ServiceInput = {
  name: string;
  kind?: "app" | "postgres";
  image?: string;
  replicas?: number;
  port?: number;
  domain?: string;
  env?: Record<string, string>;
  memoryMb?: number;
  databaseId?: string;
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
  error?: string;
  createdAt: string;
  finishedAt?: string;
};

export type OpStatus = "running" | "succeeded" | "failed";

export type BackupTarget = {
  id: string;
  name: string;
  kind: "local" | "s3";
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

export type TargetInput = Omit<BackupTarget, "id" | "kind" | "createdAt" | "ageRecipient"> & { encrypt?: boolean };

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

export type LogLine = { container: string; text: string };

export type User = { id: string; username: string; createdAt: string };

export type AuthState = { setupRequired: boolean; user?: User };

export type Status = {
  docker?: { version: string; apiVersion: string; os: string; arch: string };
  dockerError?: string;
};

/** Env values come back masked; sending the mask keeps the stored value. */
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

  projects: () => request<Project[]>("/projects"),
  project: (id: string) => request<Project>(`/projects/${id}`),
  createProject: (name: string) => request<Project>("/projects", json("POST", { name })),
  deleteProject: (id: string, confirm: string) =>
    request<void>(`/projects/${id}?confirm=${encodeURIComponent(confirm)}`, { method: "DELETE" }),

  services: (projectId: string) => request<Service[]>(`/projects/${projectId}/services`),
  service: (id: string) => request<Service>(`/services/${id}`),
  createService: (projectId: string, input: ServiceInput) =>
    request<Service>(`/projects/${projectId}/services`, json("POST", input)),
  updateService: (id: string, patch: ServicePatch) => request<Service>(`/services/${id}`, json("PATCH", patch)),
  deleteService: (id: string, confirm = "") =>
    request<void>(`/services/${id}?confirm=${encodeURIComponent(confirm)}`, { method: "DELETE" }),
  connection: (id: string) => request<Connection>(`/services/${id}/connection`),
  serviceAction: (id: string, action: "start" | "stop" | "restart") =>
    request<Service>(`/services/${id}/${action}`, { method: "POST" }),

  deploy: (serviceId: string) => request<Deployment>(`/services/${serviceId}/deploy`, { method: "POST" }),
  deployments: (serviceId: string) => request<Deployment[]>(`/services/${serviceId}/deployments`),
  deploymentLog: (id: string) => request<string>(`/deployments/${id}/log`),

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

  logsUrl: (serviceId: string, tail = 200) => `/api/services/${serviceId}/logs?tail=${tail}`,
};
