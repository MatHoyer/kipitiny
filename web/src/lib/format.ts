export type EnvRow = { key: string; value: string };

/** Parses KEY=VALUE lines into rows; blank lines and # comments are ignored. */
export function parseEnvRows(text: string): EnvRow[] {
  const rows: EnvRow[] = [];
  for (const raw of text.split("\n")) {
    const line = raw.trim();
    if (!line || line.startsWith("#")) continue;
    const i = line.indexOf("=");
    rows.push(i === -1 ? { key: line, value: "" } : { key: line.slice(0, i).trim(), value: line.slice(i + 1) });
  }
  return rows;
}

export function formatEnvRows(rows: EnvRow[]): string {
  return rows
    .filter((r) => r.key || r.value)
    .map((r) => `${r.key}=${r.value}`)
    .join("\n");
}

export const envRows = (env: Record<string, string>): EnvRow[] =>
  Object.entries(env)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([key, value]) => ({ key, value }));

/** Rows to the API map; rows without a name are dropped, the last duplicate wins. */
export const envMap = (rows: EnvRow[]): Record<string, string> =>
  Object.fromEntries(rows.filter((r) => r.key.trim()).map((r) => [r.key.trim(), r.value]));

/** A service env value referencing a project variable: {{ project.NAME }}. */
export const envRefRe = /\{\{\s*project\.([A-Za-z_][A-Za-z0-9_]*)\s*\}\}/g;

export const envRef = (name: string) => `{{ project.${name} }}`;

/** The variable a value references when it is exactly one reference. */
export function soleRef(value: string): string | null {
  const m = /^\s*\{\{\s*project\.([A-Za-z_][A-Za-z0-9_]*)\s*\}\}\s*$/.exec(value);
  return m ? m[1] : null;
}

export const refsIn = (value: string): string[] => [...value.matchAll(envRefRe)].map((m) => m[1]);

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
