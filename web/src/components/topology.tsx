import { Box, Globe, HardDrive, Lock, Network, Server, ShieldCheck, TriangleAlert } from "lucide-react";
import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { Link } from "react-router";
import { CloudflareIcon, PostgresIcon } from "@/components/brand-icons";
import { stateColors, Tag } from "@/components/common";
import { Card } from "@/components/ui/card";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { liveState, troubled } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { ServerTopology, TopoNetwork, TopoNode, TopoProject, TopoService } from "../api";

const PROXY_NETWORK = "kipitiny-proxy";

type EdgeKind = "web" | "db" | "tunnel";

/**
 * A connector between two elements marked with data-anchor. "bus" edges
 * leave a network band straight down at the target's x, as an attachment.
 */
type Edge = { from: string; to: string; kind: EdgeKind; bus?: boolean };

const edgeStyles: Record<EdgeKind, string> = {
  web: "text-sky-500",
  db: "text-[#4169E1] [stroke-dasharray:5_4]",
  tunnel: "text-orange-500",
};

/** Why a service is misconfigured on the map, or "". */
export function serviceWarning(svc: TopoService): string {
  const live = svc.containers.filter((c) => !c.retired);
  const onProxy = live.some((c) => c.endpoints.some((e) => e.network === PROXY_NETWORK));
  if (svc.kind === "postgres" && onProxy) return "Attached to the proxy network";
  if (svc.domain && live.length > 0 && !onProxy) return "Not on the proxy network: Traefik can't reach it";
  return "";
}

const serviceState = (svc: TopoService) => liveState({ stopped: !!svc.stopped, containers: svc.containers });

/** How many of the project's services are misconfigured or not running well. */
export function projectProblems(p: TopoProject): number {
  return p.services.filter((svc) => serviceWarning(svc) || troubled(serviceState(svc))).length;
}

/** A server's map in a card, with its name and any Docker error. */
export function ServerCard({
  server: s,
  showName = true,
  compact,
}: {
  server: ServerTopology;
  showName?: boolean;
  compact?: boolean;
}) {
  return (
    <Card className="gap-4 px-4 sm:px-6">
      {(showName || s.error) && (
        <header className="flex flex-wrap items-center gap-2">
          {showName && (
            <>
              <Server className="size-4 text-muted-foreground" />
              <h2 className="font-medium">{s.name}</h2>
              <Tag>{s.kind === "local" ? "this server" : "SSH"}</Tag>
            </>
          )}
          {s.error && (
            <p role="alert" className="flex items-center gap-1 text-sm text-destructive">
              <TriangleAlert className="size-3.5" />
              Docker unreachable: {s.error}
            </p>
          )}
        </header>
      )}
      <ServerMap server={s} compact={compact} />
    </Card>
  );
}

/**
 * One server's map: how traffic gets in, Traefik, the proxy network and each
 * project's network. Compact draws each project as a card linking to its own
 * map, for servers with many projects.
 */
export function ServerMap({ server: s, compact }: { server: ServerTopology; compact?: boolean }) {
  const edges: Edge[] = [];
  const proxyNet = s.networks.find((n) => n.name === PROXY_NETWORK);
  const projectNets = new Map(s.networks.filter((n) => n.projectId).map((n) => [n.projectId!, n]));

  if (s.traefik) {
    if (s.tunnel) {
      edges.push({ from: "internet", to: "cloudflared", kind: "tunnel" }, { from: "cloudflared", to: "traefik", kind: "tunnel" });
    } else {
      edges.push({ from: "internet", to: "traefik", kind: "web" });
    }
    if (compact) edges.push({ from: "traefik", to: "proxy-net", kind: "web", bus: true });
    else
      for (const p of s.projects) {
        if (p.services.some((svc) => svc.domain)) edges.push({ from: "traefik", to: `proxy-${p.id}`, kind: "web", bus: true });
      }
    if (s.manager?.domain) edges.push({ from: "traefik", to: "manager", kind: "web" });
  }
  if (!compact) {
    for (const svc of s.projects.flatMap((p) => p.services)) {
      for (const db of svc.uses) edges.push({ from: `svc-${svc.id}`, to: `svc-${db}`, kind: "db" });
    }
  }

  return (
    <Canvas edges={edges}>
      <div className="flex flex-col items-center gap-10">
        {s.traefik ? (
          <>
            <div className="flex flex-wrap items-center justify-center gap-6">
              <Node anchor="internet" className="px-4 py-2">
                <span className="flex items-center gap-2 text-sm font-medium">
                  <Globe className="size-4 text-sky-500" />
                  Internet
                </span>
              </Node>
              {s.tunnel && (
                <Node anchor="cloudflared" className="w-60">
                  <NodeHead icon={<CloudflareIcon className="size-4 text-orange-500" />} title="Cloudflare tunnel" />
                  <p className="text-xs text-muted-foreground">No public ports; forwards to Traefik's 443.</p>
                  {s.cloudflared ? <Replicas nodes={[s.cloudflared]} /> : <Missing>cloudflared container not found</Missing>}
                </Node>
              )}
            </div>
            <div className="flex flex-wrap items-start justify-center gap-6">
              <Node anchor="traefik" className="w-72">
                <NodeHead icon={<ShieldCheck className="size-4 text-sky-500" />} title="Traefik" sub="kipitiny-traefik" />
                <ul className="space-y-1">
                  {s.entrypoints.map((ep) => (
                    <li
                      key={ep.name}
                      className="flex flex-wrap items-center justify-between gap-x-2 rounded-md bg-muted px-2 py-1 font-mono text-xs"
                    >
                      <span>
                        {ep.name} :{ep.port}
                      </span>
                      <span className="text-muted-foreground">
                        {ep.hostPort ? `host :${ep.hostPort}` : "not published"}
                        {ep.redirectTo && ` → ${ep.redirectTo}`}
                      </span>
                    </li>
                  ))}
                </ul>
                {s.proxy ? <Replicas nodes={[s.proxy]} /> : <Missing>Traefik container not found</Missing>}
              </Node>
              {s.manager && (
                <Node anchor="manager" className="w-64">
                  <NodeHead icon={<Server className="size-4 text-primary" />} title="kipitiny" sub={s.manager.domain || "no domain routed"} />
                  <p title={s.manager.upstream} className="truncate font-mono text-xs text-muted-foreground">
                    upstream {s.manager.upstream}
                  </p>
                  {s.manager.container && <Replicas nodes={[s.manager.container]} />}
                </Node>
              )}
            </div>
          </>
        ) : (
          <p className="text-sm text-muted-foreground">Traefik isn't managed here: public domains are routed by your own proxy.</p>
        )}
        {compact ? (
          <ProjectsByExposure
            projects={s.projects}
            proxyNet={proxyNet}
            render={(ps) => (
              <div className="grid grid-cols-[repeat(auto-fill,minmax(15rem,1fr))] items-start gap-4">
                {ps.map((p) => (
                  <ProjectCard key={p.id} project={p} net={projectNets.get(p.id)} />
                ))}
              </div>
            )}
          />
        ) : s.projects.length === 0 ? (
          <p className="text-sm text-muted-foreground">No projects on this server.</p>
        ) : (
          <div className="flex w-full flex-wrap items-start justify-center gap-6">
            {s.projects.map((p) => (
              <ProjectBox key={p.id} id={p.id} name={p.name} net={projectNets.get(p.id)} proxyNet={proxyNet} services={p.services} />
            ))}
          </div>
        )}
      </div>
    </Canvas>
  );
}

/**
 * Projects with a public domain drawn inside the proxy network they join,
 * the others below it.
 */
function ProjectsByExposure({
  projects,
  proxyNet,
  render,
}: {
  projects: TopoProject[];
  proxyNet?: TopoNetwork;
  render: (projects: TopoProject[]) => ReactNode;
}) {
  const isPublic = (p: TopoProject) => p.services.some((svc) => svc.domain);
  const exposed = projects.filter(isPublic);
  const internal = projects.filter((p) => !isPublic(p));
  return (
    <>
      <section data-anchor="proxy-net" className="w-full space-y-4 rounded-xl border-2 border-dashed border-sky-500/50 bg-sky-500/[0.03] p-4">
        <header className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1">
          <span className="flex items-center gap-2 text-sm font-medium">
            <Network className="size-4 text-sky-500" />
            Proxy network
            <span className="font-normal text-muted-foreground max-sm:hidden">· shared with Traefik, which routes these projects’ domains</span>
          </span>
          <NetLabel net={proxyNet} name={PROXY_NETWORK} />
        </header>
        {exposed.length > 0 ? render(exposed) : <p className="text-sm text-muted-foreground">No public projects.</p>}
      </section>
      {internal.length > 0 && (
        <section className="w-full space-y-3">
          <h3 className="flex items-center gap-2 text-sm font-medium">
            <Lock className="size-4 text-muted-foreground" />
            Private projects
            <span className="font-normal text-muted-foreground max-sm:hidden">· only on their own network</span>
          </h3>
          {render(internal)}
        </section>
      )}
    </>
  );
}

const MAX_DOMAINS = 3;

function ProjectCard({ project: p, net }: { project: TopoProject; net?: TopoNetwork }) {
  const domains = p.services.flatMap((svc) => (svc.domain ? [svc.domain] : []));
  const problems = projectProblems(p);
  return (
    <Link
      to={`/projects/${p.id}?tab=map`}
      className={cn(
        "relative min-w-0 space-y-2 rounded-xl border-2 border-dashed bg-card p-3 transition-colors outline-none hover:border-foreground/30 focus-visible:ring-3 focus-visible:ring-ring/50",
        problems > 0 && "border-destructive/50",
      )}
    >
      <div className="flex items-start justify-between gap-2">
        <p title={p.name} className="truncate font-medium">
          {p.name}
        </p>
        {problems > 0 && (
          <span className="flex shrink-0 items-center gap-1 text-xs text-destructive">
            <TriangleAlert className="size-3" />
            {problems}
          </span>
        )}
      </div>
      <NetLabel net={net} name={p.network} />
      {domains.length > 0 && (
        <ul className="space-y-0.5 font-mono text-xs text-muted-foreground">
          {domains.slice(0, MAX_DOMAINS).map((d) => (
            <li key={d} title={d} className="flex min-w-0 items-center gap-1">
              <Globe className="size-3 shrink-0" />
              <span className="truncate">{d}</span>
            </li>
          ))}
          {domains.length > MAX_DOMAINS && <li>+{domains.length - MAX_DOMAINS} more</li>}
        </ul>
      )}
      {p.services.length === 0 ? (
        <p className="text-xs text-muted-foreground">No services.</p>
      ) : (
        <>
          <ServicePills services={p.services.filter((svc) => svc.domain)} isPublic />
          {/* Inside the proxy network box, make clear these stay off it. */}
          <ServicePills services={p.services.filter((svc) => !svc.domain)} labelled={domains.length > 0} />
        </>
      )}
    </Link>
  );
}

/**
 * One pill per service. Public ones carry a globe; the others, when labelled,
 * sit under a divider marked as reachable only on the project's network.
 */
function ServicePills({ services, isPublic, labelled }: { services: TopoService[]; isPublic?: boolean; labelled?: boolean }) {
  if (services.length === 0) return null;
  return (
    <div className={cn("space-y-1", labelled && "border-t border-dashed pt-2")}>
      {labelled && (
        <p className="flex items-center gap-1 text-[11px] text-muted-foreground">
          <Lock className="size-3" />
          Internal only
        </p>
      )}
      <ul className="flex flex-wrap gap-1">
        {services.map((svc) => {
          const state = serviceState(svc);
          const warning = serviceWarning(svc);
          return (
            <li
              key={svc.id}
              title={`${svc.name}: ${warning || state}`}
              className={cn(
                "inline-flex max-w-full min-w-0 items-center gap-1 rounded-full border px-1.5 py-0.5 text-[11px] text-muted-foreground",
                isPublic && "border-sky-500/40",
                warning && "border-destructive/50 text-destructive",
              )}
            >
              <span className={cn("size-1.5 shrink-0 rounded-full", stateColors[state] ?? "bg-muted-foreground/50")} />
              {isPublic && <Globe className="size-3 shrink-0 text-sky-500" />}
              {svc.kind === "postgres" && <PostgresIcon className="size-3 shrink-0 text-[#4169E1]" />}
              <span className="truncate">{svc.name}</span>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

/**
 * A project network with its services. The ones with a domain sit in an inner
 * box: the part of the project that is also on the proxy network.
 */
function ProjectBox({
  id,
  name,
  net,
  proxyNet,
  services,
}: {
  id: string;
  name: string;
  net?: TopoNetwork;
  proxyNet?: TopoNetwork;
  services: TopoService[];
}) {
  const exposed = services.filter((s) => s.domain);
  const apps = services.filter((s) => !s.domain && s.kind !== "postgres");
  const dbs = services.filter((s) => s.kind === "postgres");
  return (
    <section className="w-full min-w-0 space-y-4 rounded-xl border-2 border-dashed p-4">
      {/* Opaque so Traefik's line into the proxy box passes behind the text. */}
      <header className="relative w-fit max-w-full space-y-0.5 bg-card">
        <Link to={`/projects/${id}`} className="font-medium break-all hover:underline">
          {name}
        </Link>
        <NetLabel net={net} />
      </header>
      {services.length === 0 && <p className="text-sm text-muted-foreground">No services.</p>}
      {exposed.length > 0 && (
        <div data-anchor={`proxy-${id}`} className="space-y-3 rounded-xl border-2 border-dashed border-sky-500/50 bg-sky-500/[0.03] p-3">
          <header className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1">
            <span className="flex items-center gap-2 text-sm font-medium">
              <Network className="size-4 text-sky-500" />
              Proxy network
              <span className="font-normal text-muted-foreground max-sm:hidden">· shared with Traefik, which routes their domains</span>
            </span>
            <NetLabel net={proxyNet} name={PROXY_NETWORK} />
          </header>
          <ServiceRow services={exposed} />
        </div>
      )}
      <ServiceRow services={apps} />
      <ServiceRow services={dbs} />
    </section>
  );
}

function ServiceRow({ services }: { services: TopoService[] }) {
  if (services.length === 0) return null;
  return (
    <div className="flex flex-wrap justify-center gap-4">
      {services.map((svc) => (
        <ServiceNode key={svc.id} svc={svc} />
      ))}
    </div>
  );
}

function ServiceNode({ svc }: { svc: TopoService }) {
  const isDb = svc.kind === "postgres";
  const warning = serviceWarning(svc);
  return (
    <Node anchor={`svc-${svc.id}`} className={cn("w-56", svc.domain && "ring-sky-500/40", warning && "ring-destructive/50")}>
      <NodeHead
        icon={isDb ? <PostgresIcon className="size-4 text-[#4169E1]" /> : <Box className="size-4 text-primary" />}
        title={
          <Link to={`/services/${svc.id}`} title={svc.name} className="hover:underline">
            {svc.name}
          </Link>
        }
        sub={svc.image}
      />
      <div className="flex flex-wrap gap-1.5">
        {isDb ? (
          svc.volume && (
            <Tag className="flex max-w-full min-w-0 items-center gap-1 font-mono font-normal">
              <HardDrive className="size-3 shrink-0" />
              <span title={svc.volume} className="truncate">
                {svc.volume}
              </span>
            </Tag>
          )
        ) : (
          <Tag className="flex max-w-full min-w-0 items-center gap-1 font-mono font-normal">
            {svc.domain ? <Globe className="size-3 shrink-0" /> : <Lock className="size-3 shrink-0" />}
            <span title={svc.domain} className="truncate">
              {svc.domain || "private"}
            </span>
          </Tag>
        )}
        {(svc.port || isDb) && <Tag className="font-mono font-normal">:{isDb ? 5432 : svc.port}</Tag>}
      </div>
      {svc.stopped ? (
        <p className="text-xs text-muted-foreground">Stopped</p>
      ) : svc.containers.length === 0 ? (
        <p className="text-xs text-muted-foreground">Not deployed</p>
      ) : (
        <Replicas nodes={svc.containers} />
      )}
      {warning && <Missing>{warning}</Missing>}
    </Node>
  );
}

/** One pill per container; the tooltip lists its networks and addresses. */
function Replicas({ nodes }: { nodes: TopoNode[] }) {
  return (
    <div className="flex flex-wrap gap-1">
      {nodes.map((c) => (
        <Tooltip key={c.id}>
          <TooltipTrigger asChild>
            <span
              tabIndex={0}
              className={cn(
                "inline-flex cursor-default items-center gap-1 rounded-full border px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring/50",
                c.retired && "opacity-50",
              )}
            >
              <span className={cn("size-1.5 rounded-full", stateColors[c.health || c.state] ?? "bg-muted-foreground/50")} />
              {c.replica ? `#${c.replica}` : c.id.slice(0, 6)}
            </span>
          </TooltipTrigger>
          <TooltipContent className="max-w-xs space-y-1 font-mono text-xs">
            <p className="font-medium break-all">{c.name}</p>
            <p>
              {c.status}
              {c.health && ` · ${c.health}`}
              {c.retired && " · previous deployment"}
            </p>
            {c.endpoints.map((e) => (
              <p key={e.network}>
                {e.network} {e.ip ?? "no address"}
                {e.aliases?.length ? ` (${e.aliases.join(", ")})` : ""}
              </p>
            ))}
          </TooltipContent>
        </Tooltip>
      ))}
    </div>
  );
}

function NetLabel({ net, name }: { net?: TopoNetwork; name?: string }) {
  return (
    <p className="flex min-w-0 flex-wrap items-center gap-x-2 font-mono text-xs break-all text-muted-foreground">
      <span>{net?.name ?? name}</span>
      {net?.subnet && <span>{net.subnet}</span>}
      {net?.missing && <span className="text-destructive">missing</span>}
    </p>
  );
}

function Node({ anchor, className, children }: { anchor: string; className?: string; children: ReactNode }) {
  return (
    <div data-anchor={anchor} className={cn("relative max-w-full min-w-0 space-y-2 rounded-xl bg-card p-3 text-card-foreground shadow-xs ring-1 ring-foreground/10", className)}>
      {children}
    </div>
  );
}

function NodeHead({ icon, title, sub }: { icon: ReactNode; title: ReactNode; sub?: string }) {
  return (
    <div className="flex items-start gap-2">
      <span className="mt-0.5 shrink-0">{icon}</span>
      <div className="min-w-0 flex-1">
        <p title={typeof title === "string" ? title : undefined} className="truncate text-sm font-medium">
          {title}
        </p>
        {sub && (
          <p title={sub} className="truncate font-mono text-xs text-muted-foreground">
            {sub}
          </p>
        )}
      </div>
    </div>
  );
}

function Missing({ children }: { children: ReactNode }) {
  return (
    <p className="flex items-center gap-1 text-xs text-destructive">
      <TriangleAlert className="size-3 shrink-0" />
      {children}
    </p>
  );
}

type Path = { d: string; head: string; kind: EdgeKind };

/** Lays out its children as HTML and draws the edges between anchors in an SVG behind them. */
function Canvas({ edges, children }: { edges: Edge[]; children: ReactNode }) {
  const root = useRef<HTMLDivElement>(null);
  const [paths, setPaths] = useState<Path[]>([]);
  const key = JSON.stringify(edges);

  useLayoutEffect(() => {
    const el = root.current;
    if (!el) return;
    const draw = () => {
      const base = el.getBoundingClientRect();
      const rect = (id: string) => {
        const a = el.querySelector(`[data-anchor="${CSS.escape(id)}"]`);
        if (!a) return null;
        const r = a.getBoundingClientRect();
        return { l: r.left - base.left, r: r.right - base.left, t: r.top - base.top, b: r.bottom - base.top };
      };
      const cards = [...el.querySelectorAll<HTMLElement>('[data-anchor^="svc-"]')].map((c) => ({
        id: c.dataset.anchor!,
        ...rect(c.dataset.anchor!)!,
      }));
      const brackets = new Map<string, number>(); // per target, to fan them out
      const next: Path[] = [];
      for (const e of JSON.parse(key) as Edge[]) {
        const a = rect(e.from);
        const b = rect(e.to);
        if (!a || !b) continue;
        // A line straight down would pass behind another service and look
        // like it ends there: go around the right side instead.
        const between = cards.filter((c) => c.id !== e.from && c.id !== e.to && crosses(a, b, c));
        if (e.kind === "db" && between.length > 0) {
          const n = brackets.get(e.to) ?? 0;
          brackets.set(e.to, n + 1);
          next.push({ ...bracket(a, b, Math.max(a.r, b.r, ...between.map((c) => c.r)) + 16 + 8 * n), kind: e.kind });
          continue;
        }
        next.push({ ...route(a, b, e.bus), kind: e.kind });
      }
      setPaths(next);
    };
    draw();
    const ro = new ResizeObserver(draw);
    ro.observe(el);
    for (const child of el.querySelectorAll("[data-anchor]")) ro.observe(child);
    return () => ro.disconnect();
  }, [key]);

  return (
    <div ref={root} className="relative">
      <svg aria-hidden className="pointer-events-none absolute inset-0 size-full overflow-visible">
        {paths.map((p, i) => (
          <g key={i} className={edgeStyles[p.kind]}>
            <path d={p.d} fill="none" stroke="currentColor" strokeWidth={1.5} />
            <path d={p.head} fill="currentColor" className="[stroke-dasharray:none]" />
          </g>
        ))}
      </svg>
      {children}
    </div>
  );
}

type Rect = { l: number; r: number; t: number; b: number };

/** Whether c sits in the way of a line from a's bottom down to b's top. */
function crosses(a: Rect, b: Rect, c: Rect): boolean {
  const ax = (a.l + a.r) / 2;
  const bx = (b.l + b.r) / 2;
  return c.l < Math.max(ax, bx) + 2 && c.r > Math.min(ax, bx) - 2 && c.t < b.t && c.b > a.b;
}

/** From a's right side out to x, down, and into b's right side. */
function bracket(a: Rect, b: Rect, x: number): { d: string; head: string } {
  const r = 8;
  const y1 = (a.t + a.b) / 2;
  const y2 = (b.t + b.b) / 2;
  const x2 = b.r + 4;
  return {
    d: `M${a.r},${y1} H${x - r} Q${x},${y1} ${x},${y1 + r} V${y2 - r} Q${x},${y2} ${x - r},${y2} H${x2}`,
    head: `M${x2},${y2 - 4} L${x2},${y2 + 4} L${b.r},${y2} Z`,
  };
}

/** A curve from a to b with an arrowhead at b: downward when b is below a, sideways otherwise. */
function route(a: Rect, b: Rect, bus?: boolean): { d: string; head: string } {
  const bx = (b.l + b.r) / 2;
  if (b.t >= a.b - 1) {
    // A network band is wider than what attaches to it: go straight at the narrower one's x.
    const ax = (a.l + a.r) / 2;
    const x = a.r - a.l > b.r - b.l ? Math.min(Math.max(bx, a.l + 8), a.r - 8) : Math.min(Math.max(ax, b.l + 8), b.r - 8);
    const x1 = bus ? x : ax;
    const x2 = bus ? x : bx;
    const y1 = a.b;
    const y2 = b.t - 4;
    const my = (y1 + y2) / 2;
    return {
      d: `M${x1},${y1} C${x1},${my} ${x2},${my} ${x2},${y2}`,
      head: `M${x2 - 4},${y2} L${x2 + 4},${y2} L${x2},${b.t} Z`,
    };
  }
  // Side by side (same row, or wrapped on a narrow screen).
  const right = b.l >= (a.l + a.r) / 2;
  const x1 = right ? a.r : a.l;
  const x2 = right ? b.l - 4 : b.r + 4;
  const y1 = (a.t + a.b) / 2;
  const y2 = (b.t + b.b) / 2;
  const mx = (x1 + x2) / 2;
  const tip = right ? b.l : b.r;
  return {
    d: `M${x1},${y1} C${mx},${y1} ${mx},${y2} ${x2},${y2}`,
    head: `M${x2},${y2 - 4} L${x2},${y2 + 4} L${tip},${y2} Z`,
  };
}

/** The line styles, for a legend under a map. */
export function MapLegend({ compact }: { compact?: boolean }) {
  const items: [EdgeKind, string][] = [
    ["web", "HTTP routing"],
    ["tunnel", "Cloudflare tunnel"],
  ];
  if (!compact) items.push(["db", "Uses database (env reference)"]);
  return (
    <ul className="flex flex-wrap gap-x-5 gap-y-1 text-xs text-muted-foreground">
      {items.map(([kind, label]) => (
        <li key={kind} className="flex items-center gap-1.5">
          <svg aria-hidden width="24" height="6" className={edgeStyles[kind]}>
            <path d="M0,3 H24" stroke="currentColor" strokeWidth={1.5} />
          </svg>
          {label}
        </li>
      ))}
      <li className="flex items-center gap-1.5">
        <span className="size-3 rounded-sm border-2 border-dashed" />
        Project network
      </li>
      <li className="flex items-center gap-1.5">
        <span className="size-3 rounded-sm border-2 border-dashed border-sky-500/50" />
        Proxy network
      </li>
    </ul>
  );
}
