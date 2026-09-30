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

  logsUrl: (serviceId: string, tail = 200) => `/api/services/${serviceId}/logs?tail=${tail}`,
};
