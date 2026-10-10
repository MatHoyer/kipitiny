import { demoService } from "./backend";

/*
 * The two things the UI reads without fetch: service logs (EventSource) and
 * terminals (WebSocket). Both are played in the browser: plausible log lines
 * for the service's image, and a tiny shell.
 */

const pick = <T,>(a: T[]) => a[Math.floor(Math.random() * a.length)];
const ip = () => `203.0.113.${1 + Math.floor(Math.random() * 250)}`;
const pad = (n: number, w = 2) => String(n).padStart(w, "0");

function logLine(kind: string, image: string, at: Date): string {
  const iso = at.toISOString();
  if (kind === "postgres") {
    const pid = 100 + Math.floor(Math.random() * 900);
    return pick([
      `${iso.replace("T", " ").replace("Z", "")} UTC [${pid}] LOG:  checkpoint starting: time`,
      `${iso.replace("T", " ").replace("Z", "")} UTC [${pid}] LOG:  checkpoint complete: wrote ${10 + Math.floor(Math.random() * 90)} buffers (0.${Math.floor(Math.random() * 9)}%); 0 WAL file(s) added`,
      `${iso.replace("T", " ").replace("Z", "")} UTC [${pid}] LOG:  connection authorized: user=app database=postgres application_name=api`,
      `${iso.replace("T", " ").replace("Z", "")} UTC [${pid}] LOG:  duration: ${(Math.random() * 40).toFixed(3)} ms  statement: SELECT * FROM invoices WHERE user_id = $1`,
    ]);
  }
  if (kind === "redis") {
    const d = at.toUTCString().split(" ");
    return `1:M ${d[1]} ${d[2]} ${d[3]} ${d[4]}.${pad(at.getUTCMilliseconds(), 3)} ${pick(["* 100 changes in 300 seconds. Saving...", "* Background saving started by pid 42", "* DB saved on disk", "* Background saving terminated with success"])}`;
  }
  if (image.startsWith("nginx")) {
    const path = pick(["/", "/pricing", "/assets/app-3f9c1a.js", "/assets/app-8b2e44.css", "/favicon.svg", "/blog/launch", "/login"]);
    const code = Math.random() < 0.04 ? 404 : path === "/login" && Math.random() < 0.3 ? 302 : 200;
    const months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
    return `${ip()} - - [${pad(at.getUTCDate())}/${months[at.getUTCMonth()]}/${at.getUTCFullYear()}:${pad(at.getUTCHours())}:${pad(at.getUTCMinutes())}:${pad(at.getUTCSeconds())} +0000] "GET ${path} HTTP/1.1" ${code} ${200 + Math.floor(Math.random() * 40000)} "-" "Mozilla/5.0"`;
  }
  // A Node.js API logging JSON.
  const r = Math.random();
  if (r < 0.03) return JSON.stringify({ level: "error", time: iso, msg: "stripe webhook signature mismatch", route: "/v1/webhooks/stripe" });
  if (r < 0.1) return JSON.stringify({ level: "warn", time: iso, msg: "slow query", ms: 180 + Math.floor(Math.random() * 400), sql: "SELECT … FROM invoices" });
  const route = pick(["GET /v1/me", "GET /v1/invoices", "POST /v1/sessions", "GET /v1/plans", "GET /healthz", "POST /v1/invoices/pay"]);
  return JSON.stringify({ level: "info", time: iso, msg: route, status: route.startsWith("POST /v1/sessions") && Math.random() < 0.2 ? 401 : 200, ms: 2 + Math.floor(Math.random() * 40) });
}

const isLogs = (url: URL) => /\/api\/services\/[^/]+\/logs$/.test(url.pathname);

/** An EventSource whose messages are generated log lines; other URLs use the real one. */
export function fakeEventSource(Real: typeof EventSource): typeof EventSource {
  return class extends EventTarget {
    static readonly CONNECTING = 0;
    static readonly OPEN = 1;
    static readonly CLOSED = 2;
    readonly url: string;
    readonly withCredentials = false;
    readyState = 0;
    onopen: ((e: Event) => void) | null = null;
    onmessage: ((e: MessageEvent) => void) | null = null;
    onerror: ((e: Event) => void) | null = null;
    private timers: ReturnType<typeof setTimeout>[] = [];

    constructor(url: string | URL, init?: EventSourceInit) {
      super();
      const u = new URL(String(url), location.href);
      this.url = u.href;
      if (!isLogs(u)) return new Real(url, init) as never;
      const svc = demoService(u.pathname.split("/").at(-2)!);
      const tail = Number(u.searchParams.get("tail")) || 200;
      this.timers.push(
        setTimeout(() => {
          this.readyState = 1;
          this.onopen?.(new Event("open"));
          if (!svc || svc.containers.length === 0) return this.finish();
          const start = Date.now() - 3_600_000;
          const history = Math.min(tail, 120);
          for (let i = 0; i < history; i++) this.emit(svc, new Date(start + (i / history) * 3_590_000));
          const live = () => {
            this.emit(svc, new Date());
            this.timers.push(setTimeout(live, 800 + Math.random() * 2200));
          };
          live();
        }, 80),
      );
    }

    private emit(svc: NonNullable<ReturnType<typeof demoService>>, at: Date) {
      const data = JSON.stringify({ container: pick(svc.containers), time: at.toISOString(), text: logLine(svc.kind, svc.image, at) });
      const e = new MessageEvent("message", { data });
      this.onmessage?.(e);
      this.dispatchEvent(e);
    }

    private finish() {
      this.dispatchEvent(new Event("end"));
    }

    close() {
      this.readyState = 2;
      for (const t of this.timers) clearTimeout(t);
    }
  } as unknown as typeof EventSource;
}

const isTerminal = (url: URL) => /\/api\/(services|servers)\/[^/]+\/terminal$/.test(url.pathname);

/** A WebSocket to a tiny simulated shell; other URLs use the real one. */
export function fakeWebSocket(Real: typeof WebSocket): typeof WebSocket {
  return class extends EventTarget {
    static readonly CONNECTING = 0;
    static readonly OPEN = 1;
    static readonly CLOSING = 2;
    static readonly CLOSED = 3;
    readonly url: string;
    binaryType: BinaryType = "blob";
    readyState = 0;
    protocol = "";
    extensions = "";
    bufferedAmount = 0;
    onopen: ((e: Event) => void) | null = null;
    onmessage: ((e: MessageEvent) => void) | null = null;
    onclose: ((e: CloseEvent) => void) | null = null;
    onerror: ((e: Event) => void) | null = null;
    private line = "";
    private shell!: Shell;

    constructor(url: string | URL, protocols?: string | string[]) {
      super();
      const u = new URL(String(url), location.href);
      this.url = u.href;
      if (!isTerminal(u)) return new Real(url, protocols) as never;
      const parts = u.pathname.split("/");
      const sh = u.searchParams.get("shell");
      this.shell = parts.includes("servers") ? hostShell() : serviceShell(parts.at(-2)!, !sh || sh === "auto" ? "sh" : sh);
      setTimeout(() => {
        this.readyState = 1;
        this.onopen?.(new Event("open"));
        this.write(`${this.shell.banner}${this.shell.prompt}`);
      }, 120);
    }

    private write(text: string) {
      const data = new TextEncoder().encode(text.replace(/\n/g, "\r\n")).buffer;
      this.onmessage?.(new MessageEvent("message", { data }));
    }

    send(raw: string) {
      const m = JSON.parse(raw) as { type: string; data?: string };
      if (m.type !== "input" || this.readyState !== 1) return;
      for (const ch of m.data ?? "") {
        if (ch === "\r") {
          const cmd = this.line.trim();
          this.line = "";
          this.write("\n");
          if (cmd === "exit") {
            this.onmessage?.(new MessageEvent("message", { data: JSON.stringify({ type: "exit", code: 0 }) }));
            return this.close();
          }
          const out = cmd ? this.shell.run(cmd) : "";
          this.write(`${out}${out && !out.endsWith("\n") ? "\n" : ""}${this.shell.prompt}`);
        } else if (ch === "\x7f") {
          if (this.line) {
            this.line = this.line.slice(0, -1);
            this.write("\b \b");
          }
        } else if (ch === "\x03") {
          this.line = "";
          this.write(`^C\n${this.shell.prompt}`);
        } else if (ch === "\x0c") {
          this.write(`\x1b[2J\x1b[H${this.shell.prompt}${this.line}`);
        } else if (ch >= " ") {
          this.line += ch;
          this.write(ch);
        }
      }
    }

    close() {
      if (this.readyState === 3) return;
      this.readyState = 3;
      this.onclose?.(new CloseEvent("close", { code: 1000 }));
    }
  } as unknown as typeof WebSocket;
}

type Shell = { banner: string; prompt: string; run: (cmd: string) => string };

function serviceShell(serviceId: string, shell: string): Shell {
  const svc = demoService(serviceId);
  const node = svc?.image.startsWith("node");
  const host = svc?.hostId || "container";
  const cwd = node ? "/app" : svc?.kind === "postgres" ? "/var/lib/postgresql/data" : svc?.kind === "redis" ? "/data" : "/usr/share/nginx/html";
  const files = node
    ? "dist  node_modules  package.json  package-lock.json"
    : svc?.kind === "postgres"
      ? "PG_VERSION  base  global  pg_hba.conf  pg_wal  postgresql.conf"
      : svc?.kind === "redis"
        ? "dump.rdb"
        : "50x.html  assets  favicon.svg  index.html";
  const common = common_(host);
  return {
    banner: `\x1b[90m# ${shell} in ${svc?.containers[0] ?? "the container"} (a simulated shell: this demo runs no containers)\x1b[0m\n\x1b[90m# try: ls, ps, env, df -h, ${node ? "node -v, cat package.json" : svc?.kind === "postgres" ? "psql -c 'select count(*) from users'" : svc?.kind === "redis" ? "redis-cli dbsize" : "nginx -v"}, exit\x1b[0m\n`,
    prompt: `\x1b[32m${host}\x1b[0m:\x1b[34m${cwd}\x1b[0m# `,
    run: (cmd) => {
      const [bin, ...args] = cmd.split(/\s+/);
      switch (bin) {
        case "ls":
          return files;
        case "pwd":
          return cwd;
        case "env":
          return Object.entries({ HOSTNAME: host, PATH: "/usr/local/bin:/usr/bin:/bin", ...svc?.env })
            .map(([k, v]) => `${k}=${v}`)
            .join("\n");
        case "ps":
          return node
            ? "PID   USER     TIME  COMMAND\n    1 node      2:13 node dist/server.js\n   48 root      0:00 sh\n   52 root      0:00 ps"
            : svc?.kind === "postgres"
              ? "PID   USER     TIME  COMMAND\n    1 postgres  0:41 postgres\n   27 postgres  0:02 postgres: checkpointer\n   28 postgres  0:00 postgres: background writer\n   61 root      0:00 sh"
              : svc?.kind === "redis"
                ? "PID   USER     TIME  COMMAND\n    1 redis     0:19 redis-server *:6379\n   33 root      0:00 sh"
                : "PID   USER     TIME  COMMAND\n    1 root      0:00 nginx: master process nginx -g daemon off;\n   29 nginx     0:03 nginx: worker process\n   35 root      0:00 sh";
        case "node":
          return node && args[0] === "-v" ? "v22.20.0" : `sh: ${bin}: not found`;
        case "cat":
          if (node && args[0] === "package.json") return '{\n  "name": "acme-api",\n  "version": "1.8.2",\n  "scripts": { "start": "node dist/server.js", "migrate": "node dist/migrate.js" }\n}';
          return args[0] ? `cat: can't open '${args[0]}': No such file or directory` : "";
        case "psql":
          return svc?.kind === "postgres" ? " count\n-------\n   140\n(1 row)" : `sh: ${bin}: not found`;
        case "redis-cli":
          return svc?.kind === "redis" ? "(integer) 28" : `sh: ${bin}: not found`;
        case "nginx":
          return !node && svc?.kind === "app" ? "nginx version: nginx/1.27.5" : `sh: ${bin}: not found`;
        default:
          return common(bin, args);
      }
    },
  };
}

function hostShell(): Shell {
  const common = common_("server");
  return {
    banner: "\x1b[90m# root shell on This server (a simulated shell: this demo has no server)\x1b[0m\n\x1b[90m# try: docker ps, df -h, free -m, uptime, exit\x1b[0m\n",
    prompt: "\x1b[31mroot@server\x1b[0m:\x1b[34m~\x1b[0m# ",
    run: (cmd) =>
      cmd.startsWith("docker ps")
        ? "CONTAINER ID   IMAGE                COMMAND                  STATUS                  NAMES\n3f2a9c1b8d7e   node:22-alpine       \"docker-entrypoint.s…\"   Up 2 days (healthy)     acme-api-1-c95c5f\n9b1e4d2a7c3f   node:22-alpine       \"docker-entrypoint.s…\"   Up 2 days (healthy)     acme-api-2-c95c5f\n1a7d3e9f2b4c   nginx:1.27-alpine    \"/docker-entrypoint.…\"   Up 2 days               acme-web-1-8d2e11\n5c8b2f1e9a6d   postgres:17-alpine   \"docker-entrypoint.s…\"   Up 9 days (healthy)     acme-db-1\n7e4a1c9b3d2f   redis:8-alpine       \"docker-entrypoint.s…\"   Up 9 days (healthy)     acme-cache-1\n2d9f6b3a8e1c   traefik:v3.7         \"/entrypoint.sh --pr…\"   Up 9 days               kipitiny-traefik\n8f3c7a2e5b9d   ghcr.io/mathoyer/kipitiny:0.20.0   \"/kipitiny\"   Up 9 days (healthy)   kipitiny-manager"
        : common(cmd.split(/\s+/)[0], cmd.split(/\s+/).slice(1)),
  };
}

function common_(host: string) {
  return (bin: string, args: string[]): string => {
    switch (bin) {
      case "help":
        return "This is a simulated shell. Try: ls, ps, env, df -h, free -m, uptime, date, whoami, hostname, echo, clear, exit";
      case "whoami":
        return "root";
      case "hostname":
        return host;
      case "date":
        return new Date().toUTCString();
      case "uptime":
        return " 12:04:11 up 9 days,  3:12,  0 users,  load average: 0.08, 0.06, 0.02";
      case "echo":
        return args.join(" ");
      case "df":
        return "Filesystem      Size  Used Avail Use% Mounted on\noverlay          38G  9.1G   27G  26% /\n/dev/sda1        38G  9.1G   27G  26% /data";
      case "free":
        return "              total        used        free      shared  buff/cache   available\nMem:           1967         612         281           3        1073        1185\nSwap:             0           0           0";
      case "clear":
        return "\x1b[2J\x1b[H";
      default:
        return `sh: ${bin}: not found`;
    }
  };
}
