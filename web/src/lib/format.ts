/** An env entry; a secret's saved value is write-only (it reads back masked). */
export type EnvRow = { key: string; value: string; secret: boolean };

/**
 * Parses KEY=VALUE lines into rows; blank lines and other # comments are
 * ignored. Lines after a "# secrets" line are secrets ("# variables" switches
 * back).
 */
export function parseEnvRows(text: string): EnvRow[] {
  const rows: EnvRow[] = [];
  let secret = false;
  for (const raw of text.split("\n")) {
    const line = raw.trim();
    if (/^#\s*secrets\s*$/i.test(line)) secret = true;
    else if (/^#\s*variables\s*$/i.test(line)) secret = false;
    if (!line || line.startsWith("#")) continue;
    const i = line.indexOf("=");
    rows.push({ key: i === -1 ? line : line.slice(0, i).trim(), value: i === -1 ? "" : line.slice(i + 1), secret });
  }
  return rows;
}

/** Variables, then secrets under a "# secrets" line. */
export function formatEnvRows(rows: EnvRow[]): string {
  const line = (r: EnvRow) => `${r.key}=${r.value}`;
  const kept = rows.filter((r) => r.key || r.value);
  const vars = kept.filter((r) => !r.secret).map(line);
  const secrets = kept.filter((r) => r.secret).map(line);
  return [...vars, ...(secrets.length ? ["# secrets", ...secrets] : [])].join("\n");
}

export const envRows = (env: Record<string, string>, secrets: string[] = []): EnvRow[] =>
  Object.entries(env)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([key, value]) => ({ key, value, secret: secrets.includes(key) }));

/** Rows to the API map; rows without a name are dropped, the last duplicate wins. */
export const envMap = (rows: EnvRow[]): Record<string, string> =>
  Object.fromEntries(rows.filter((r) => r.key.trim()).map((r) => [r.key.trim(), r.value]));

/** Secret names; a password manager reference is never one (it holds no secret). */
export const envSecrets = (rows: EnvRow[]): string[] =>
  rows.filter((r) => r.secret && r.key.trim() && !managerRef(r.value)).map((r) => r.key.trim());

/**
 * A reference in a service env value: {{ project.NAME }} for a project
 * variable or secret, {{ db.SERVICE.FIELD }} for a database credential,
 * {{ scheme://… }} for a secret in a password manager.
 */
const refSrc = String.raw`\{\{\s*(?:project\.([A-Za-z_][A-Za-z0-9_]*)|db\.([a-z0-9-]+)\.([A-Z]+)|([a-z][a-z0-9+.-]*://[^{}]+?))\s*\}\}`;

export type EnvReference =
  | { kind: "project"; name: string }
  | { kind: "db"; db: string; field: string }
  | { kind: "secret"; ref: string };

/** Credentials a database reference can name. */
export const dbFields = ["URL", "HOST", "PORT", "USER", "PASSWORD", "DATABASE"] as const;

export const envRef = (name: string) => `{{ project.${name} }}`;
export const dbRef = (db: string, field: string) => `{{ db.${db}.${field} }}`;

const toRef = (m: RegExpMatchArray): EnvReference =>
  m[1] ? { kind: "project", name: m[1] } : m[4] ? { kind: "secret", ref: m[4] } : { kind: "db", db: m[2], field: m[3] };

/** How a reference is named in messages: project.NAME, db.SERVICE.FIELD, scheme://…. */
export const refName = (r: EnvReference) =>
  r.kind === "project" ? `project.${r.name}` : r.kind === "secret" ? r.ref : `db.${r.db}.${r.field}`;

/** The reference a value is made of, when it is exactly one. */
export function soleRef(value: string): EnvReference | null {
  const m = new RegExp(`^\\s*${refSrc}\\s*$`).exec(value);
  return m ? toRef(m) : null;
}

/** A value that is exactly one password manager reference: its scheme and path (vault, item, field). */
export function managerRef(value: string): { scheme: string; path: string[] } | null {
  const r = soleRef(value);
  if (r?.kind !== "secret") return null;
  const i = r.ref.indexOf("://");
  return { scheme: r.ref.slice(0, i), path: r.ref.slice(i + 3).split("/").filter(Boolean) };
}

export const refsIn = (value: string): EnvReference[] => [...value.matchAll(new RegExp(refSrc, "g"))].map(toRef);

/** A value cut into its literal text and its references, in order. */
export type ValuePart = { text: string } | { ref: EnvReference; raw: string };

export function splitRefs(value: string): ValuePart[] {
  const parts: ValuePart[] = [];
  let at = 0;
  for (const m of value.matchAll(new RegExp(refSrc, "g"))) {
    if (m.index > at) parts.push({ text: value.slice(at, m.index) });
    parts.push({ ref: toRef(m), raw: m[0] });
    at = m.index + m[0].length;
  }
  if (at < value.length) parts.push({ text: value.slice(at) });
  return parts;
}

export function timeAgo(iso: string): string {
  const s = Math.round((Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  if (s < 86400) return `${Math.round(s / 3600)}h ago`;
  return new Date(iso).toLocaleDateString();
}

/** Aggregate state of a service from its containers. */
export function serviceState(containers: { state: string; health?: string }[]): string {
  if (containers.length === 0) return "not deployed";
  if (containers.some((c) => c.health === "unhealthy")) return "unhealthy";
  if (containers.some((c) => c.health === "starting")) return "starting";
  if (containers.every((c) => c.state === "running")) return "running";
  if (containers.some((c) => c.state === "running")) return "degraded";
  return containers[0].state;
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

export function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms} ms`;
  const s = ms / 1000;
  return s < 60 ? `${s.toFixed(1)} s` : `${Math.floor(s / 60)}m ${Math.round(s % 60)}s`;
}

/** A service's state as lists show it: stopped on purpose, or from its live containers. */
export function liveState(svc: { stopped: boolean; containers: { state: string; health?: string; retired?: boolean }[] }): string {
  return svc.stopped ? "stopped" : serviceState(svc.containers.filter((c) => !c.retired));
}

/** States that need attention. */
export const troubled = (state: string) => ["unhealthy", "degraded", "failed", "dead", "exited"].includes(state);

/** Groups items (newest first) under "Today", "Yesterday" or their date. */
export function byDay<T extends { createdAt: string }>(items: T[]): [string, T[]][] {
  const groups = new Map<string, T[]>();
  const today = new Date().toDateString();
  const yesterday = new Date(Date.now() - 86_400_000).toDateString();
  for (const it of items) {
    const d = new Date(it.createdAt);
    const key =
      d.toDateString() === today
        ? "Today"
        : d.toDateString() === yesterday
          ? "Yesterday"
          : d.toLocaleDateString([], { weekday: "long", day: "numeric", month: "long", year: "numeric" });
    groups.set(key, [...(groups.get(key) ?? []), it]);
  }
  return [...groups];
}

/** Whether rows hold the saved env, ignoring order and nameless rows. */
export function sameEnv(rows: EnvRow[], env: Record<string, string>, secrets: string[] = []) {
  const norm = (rs: EnvRow[]) => JSON.stringify(envRows(envMap(rs), envSecrets(rs)));
  return norm(rows) === norm(envRows(env, secrets));
}
