import { Handle, Position, type NodeProps } from "@xyflow/react";
import { GitFork, Globe, HardDrive, Network, Plus, Server, ShieldCheck, TriangleAlert } from "lucide-react";
import type { ReactNode } from "react";
import { CloudflareIcon } from "@/components/brand-icons";
import { stateColors } from "@/components/common";
import { ReachIcon, reachOf } from "@/components/reach";
import { ServiceIcon } from "@/components/service-icon";
import { projectProblems, serviceState, serviceWarning } from "@/components/topology";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { NewServiceDialog } from "@/routes/Project";
import { databasePorts, isDatabase } from "@/api";
import type { InfraData, MapNode, NetworkData, ProjectData, ServerData, ServiceData } from "./build";

type Props<T> = NodeProps<MapNode> & { data: T };

/** Handles are invisible until hovered: the canvas reads as a diagram, not a wiring tool. */
const handle = "!size-2 !border-0 !bg-muted-foreground/40 opacity-0 transition-opacity group-hover:opacity-100";

function Card({ selected, className, children }: { selected?: boolean; className?: string; children: ReactNode }) {
  return (
    <div
      className={cn(
        "group size-full overflow-hidden rounded-xl bg-card p-3 text-card-foreground shadow-xs ring-1 ring-foreground/10 transition-shadow",
        selected && "ring-2 ring-primary",
        className,
      )}
    >
      {children}
    </div>
  );
}

function ServiceNode({ data: { svc }, selected }: Props<ServiceData>) {
  const isDb = isDatabase(svc.kind);
  const warning = serviceWarning(svc);
  const state = serviceState(svc);
  const live = svc.containers.filter((c) => !c.retired);
  return (
    <Card selected={selected} className={cn("space-y-2", svc.domain && "ring-sky-500/40", warning && "ring-destructive/60")}>
      <Handle type="target" position={Position.Top} className={handle} />
      <div className="flex items-start gap-2">
        <ServiceIcon service={svc} className="mt-0.5 size-4 shrink-0 text-primary" />
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-medium">{svc.name}</p>
          <p title={svc.image} className="truncate font-mono text-[11px] text-muted-foreground">
            {svc.image}
          </p>
        </div>
        <span title={state} className={cn("mt-1.5 size-2 shrink-0 rounded-full", stateColors[state] ?? "bg-muted-foreground/50")} />
      </div>
      <p className="flex min-w-0 items-center gap-1 font-mono text-[11px] text-muted-foreground">
        {isDb ? <HardDrive className="size-3 shrink-0" /> : <ReachIcon service={svc} className="size-3 shrink-0" />}
        <span className="truncate">{isDb ? `:${databasePorts[svc.kind as keyof typeof databasePorts]}` : reachOf(svc).label}</span>
        {!isDb && reachOf(svc).kind === "http" ? <span className="shrink-0">:{svc.port}</span> : null}
      </p>
      {warning ? (
        <p className="flex items-center gap-1 truncate text-[11px] text-destructive">
          <TriangleAlert className="size-3 shrink-0" />
          {warning}
        </p>
      ) : (
        <div className="flex items-center gap-1">
          {svc.stopped ? (
            <span className="text-[11px] text-muted-foreground">Stopped</span>
          ) : live.length === 0 ? (
            <span className="text-[11px] text-muted-foreground">Not deployed</span>
          ) : (
            live.map((c) => (
              <span key={c.id} title={`${c.name}: ${c.health || c.state}`} className={cn("size-2 rounded-full", stateColors[c.health || c.state] ?? "bg-muted-foreground/50")} />
            ))
          )}
        </div>
      )}
      <Handle type="source" position={Position.Bottom} className={handle} />
    </Card>
  );
}

function ProjectFrame({ data: { project, net }, selected }: Props<ProjectData>) {
  const problems = projectProblems(project);
  return (
    <div className={cn("size-full rounded-2xl border-2 border-dashed bg-muted/20", selected && "border-primary/60", problems > 0 && "border-destructive/40")}>
      <header className="frame-handle flex cursor-grab items-center gap-2 px-4 pt-3">
        <span className="shrink-0 text-sm font-medium">{project.name}</span>
        {net?.subnet && <span className="truncate font-mono text-[11px] text-muted-foreground">{net.subnet}</span>}
        {net?.missing && <span className="text-[11px] text-destructive">network missing</span>}
        <span className="ml-auto flex shrink-0 items-center gap-2">
          {problems > 0 && (
            <span className="flex items-center gap-1 text-xs text-destructive">
              <TriangleAlert className="size-3" />
              {problems}
            </span>
          )}
          {project.gitPath ? (
            // Its services come from the compose file: none to add here.
            <span title={`Follows ${project.gitPath} in git: add and change services in that file.`} className="flex items-center gap-1 text-[11px] text-muted-foreground">
              <GitFork className="size-3" />
              {project.gitPath}
            </span>
          ) : (
            <NewServiceDialog
              projectId={project.id}
              stay
              trigger={
                <Button variant="ghost" size="icon-sm" title="Add a service" aria-label={`Add a service to ${project.name}`} className="nodrag size-6">
                  <Plus />
                </Button>
              }
            />
          )}
        </span>
      </header>
      {project.services.length === 0 && <p className="px-4 pt-2 text-xs text-muted-foreground">No services.</p>}
    </div>
  );
}

function ServerFrame({ data: { server }, selected }: Props<ServerData>) {
  return (
    <div className={cn("size-full rounded-3xl border bg-muted/10", selected && "border-primary/60")}>
      <header className="frame-handle flex cursor-grab items-center gap-2 px-6 pt-4 text-sm font-medium">
        <Server className="size-4 text-muted-foreground" />
        {server.name}
        {server.error && <span className="truncate text-xs font-normal text-destructive">{server.error}</span>}
      </header>
    </div>
  );
}

const infraLabels: Record<InfraData["kind"], { icon: ReactNode; title: string }> = {
  internet: { icon: <Globe className="size-4 text-sky-500" />, title: "Internet" },
  tunnel: { icon: <CloudflareIcon className="size-4 text-orange-500" />, title: "Cloudflare tunnel" },
  traefik: { icon: <ShieldCheck className="size-4 text-sky-500" />, title: "Traefik" },
  manager: { icon: <Server className="size-4 text-primary" />, title: "kipitiny" },
};

function InfraNode({ data: { kind, server: s }, selected }: Props<InfraData>) {
  const { icon, title } = infraLabels[kind];
  const container = kind === "traefik" ? s.proxy : kind === "tunnel" ? s.cloudflared : kind === "manager" ? s.manager?.container : undefined;
  const missing = (kind === "traefik" && !s.proxy) || (kind === "tunnel" && !s.cloudflared);
  return (
    <Card selected={selected} className="space-y-1.5">
      {kind !== "internet" && <Handle type="target" position={Position.Top} className={handle} />}
      <p className="flex items-center gap-2 text-sm font-medium">
        {icon}
        {title}
        {container && <span className={cn("ml-auto size-2 rounded-full", stateColors[container.health || container.state] ?? "bg-muted-foreground/50")} />}
      </p>
      {kind === "traefik" && (
        <div className="flex flex-wrap gap-1">
          {s.entrypoints.map((ep) => (
            <span key={ep.name} className="rounded-md bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">
              :{ep.port}
              {ep.hostPort ? ` ← ${ep.hostPort}` : ""}
            </span>
          ))}
        </div>
      )}
      {kind === "manager" && <p className="truncate font-mono text-[11px] text-muted-foreground">{s.manager?.domain || "no domain routed"}</p>}
      {kind === "tunnel" && <p className="text-[11px] text-muted-foreground">No public ports</p>}
      {kind === "internet" && <p className="text-[11px] text-muted-foreground">{s.tunnel ? "Through Cloudflare" : "Ports 80 and 443"}</p>}
      {missing && (
        <p className="flex items-center gap-1 text-[11px] text-destructive">
          <TriangleAlert className="size-3" />
          container not found
        </p>
      )}
      <Handle type="source" position={Position.Bottom} className={handle} />
    </Card>
  );
}

function NetworkNode({ data: { net, members }, selected }: Props<NetworkData>) {
  return (
    <Card selected={selected} className={cn("flex items-center gap-2 rounded-full px-4 py-2 ring-2 ring-violet-500/40", selected && "ring-violet-500")}>
      <Handle type="target" position={Position.Top} className={handle} />
      <Network className="size-4 shrink-0 text-violet-500" />
      <div className="min-w-0">
        <p className="truncate text-sm font-medium">{net.custom}</p>
        <p className="truncate font-mono text-[11px] text-muted-foreground">
          {net.missing ? <span className="text-destructive">missing · </span> : net.subnet && `${net.subnet} · `}
          {members} service{members === 1 ? "" : "s"}
        </p>
      </div>
    </Card>
  );
}

export const nodeTypes = { service: ServiceNode, project: ProjectFrame, server: ServerFrame, infra: InfraNode, network: NetworkNode };
