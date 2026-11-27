import type { Route } from "./ops";
import { HttpError, containers, db, dbDemo, newId, now, projectOf, save } from "./state";
import { routesA } from "./routes-a";
import { routesB } from "./routes-b";
import { routesC } from "./routes-c";
import { routesD } from "./routes-d";
/*
 * The demo's backend: answers the manager's /api from state kept in this
 * browser. Topology, containers and stats are derived from the services so
 * the UI behaves as on a real server; anything not faked answers 501. The
 * routes live in routes-*.ts, the state and helpers they share next to them.
 */

const routes: Route[] = [...routesA, ...routesB, ...routesC, ...routesD];

/** What the log stream and terminal of a service need to look real. */
export function demoService(id: string) {
  const s = db.services.find((x) => x.id === id);
  if (!s) return null;
  const p = projectOf(s.projectId);
  // Env as the container sees it: references resolved, secrets masked.
  const env = Object.fromEntries(
    Object.entries(s.env).map(([k, v]) => [
      k,
      s.secrets.includes(k) || /PASSWORD|SECRET/.test(k)
        ? "********"
        : v.replace(/\{\{\s*db\.([\w-]+)\.URL\s*\}\}/g, (_, name: string) => {
            const d = db.services.find((x) => x.projectId === s.projectId && x.name === name);
            return (dbDemo[d?.kind ?? "postgres"] ?? dbDemo.postgres).url(name, "********");
          }).replace(/\{\{\s*project\.(\w+)\s*\}\}/g, (_, key: string) => p.env[key] ?? ""),
    ]),
  );
  const cts = containers(s);
  return { name: s.name, project: p.name, kind: s.kind, image: s.image, env, port: s.port, containers: cts.map((c) => c.name), hostId: cts[0]?.id.slice(0, 12) ?? "" };
}

/** Records a change like the manager's audit log: the route's pattern, and what it targeted. */
const knownIds = () =>
  new Set<string>(
    [db.projects, db.services, db.networks, db.backups, db.schedules, db.domains, db.registries, db.channels, db.tokens, db.deployments].flatMap((list) => list.map((x) => x.id)),
  );

/** Names for IDs, taken before a change (a deleted service still reads by name). */
const names = () => new Map<string, string>([...db.services, ...db.projects].map((x) => [x.id, x.name]));

function audit(method: string, path: string, status: number, ids: Set<string>, named: Map<string, string>, error?: string) {
  if (path.startsWith("/auth/")) return;
  let target = "";
  const pattern = path
    .split("/")
    .map((seg, i) => {
      // New entities aren't listed yet when created: their parent's ID is.
      if (i < 2 || !(ids.has(seg) || /^[a-z]+-[a-z0-9]{6,}$/.test(seg))) return seg;
      target ||= named.get(seg) ?? seg;
      return "{id}";
    })
    .join("/");
  db.audit.unshift({ id: newId("au"), actor: "user:demo", action: `${method} /api${pattern}`, target, status: status === 200 ? (method === "POST" ? 201 : 200) : status, error, createdAt: now() });
  db.audit = db.audit.slice(0, 200);
  save();
}

/** Answers one /api request, as the manager would. */
export async function handle(method: string, url: URL, body: unknown): Promise<Response> {
  const path = url.pathname.replace(/^.*?\/api/, "");
  const json = (status: number, data: unknown) =>
    new Response(data === undefined ? null : typeof data === "string" ? data : JSON.stringify(data), {
      status: data === undefined && status === 200 ? 204 : status,
      headers: { "Content-Type": typeof data === "string" ? "text/plain" : "application/json" },
    });
  for (const [m, re, handler] of routes) {
    if (m !== method) continue;
    const match = path.match(re);
    if (!match) continue;
    const ids = knownIds();
    const named = names();
    try {
      const out = await handler({ m: match, q: url.searchParams, body });
      if (method !== "GET") audit(method, path, 200, ids, named);
      return out instanceof Response ? out : json(200, out);
    } catch (e) {
      if (e instanceof HttpError) {
        if (method !== "GET") audit(method, path, e.status, ids, named, e.message);
        return json(e.status, { error: e.message });
      }
      throw e;
    }
  }
  console.debug(`demo: no fake for ${method} ${path}`);
  return json(501, { error: "Not available in the demo: it runs in your browser, without a server." });
}
