import { ExternalLink, Globe, Network, X } from "lucide-react";
import type { ReactNode } from "react";
import { Link } from "react-router";
import { Mono, StateBadge } from "@/components/common";
import { ServiceIcon } from "@/components/service-icon";
import { Replicas, serviceState, serviceWarning } from "@/components/topology";
import { Button } from "@/components/ui/button";
import type { ServerTopology } from "@/api";
import type { MapNode } from "./build";

/** Details of the selected node, over the canvas's right edge. */
export function NodePanel({ node, servers, onClose }: { node: MapNode; servers: ServerTopology[]; onClose: () => void }) {
  const d = node.data;
  const server = servers.find((s) => s.id === ("serverId" in d ? d.serverId : d.server.id));
  const networks = server?.networks ?? [];
  let title: ReactNode = null;
  let body: ReactNode = null;

  switch (d.kind) {
    case "service": {
      const { svc, project } = d;
      const warning = serviceWarning(svc);
      const joined = networks.filter((n) => n.customId && svc.networks.includes(n.customId));
      const uses = project.services.filter((s) => svc.uses.includes(s.id));
      title = (
        <span className="flex min-w-0 items-center gap-2">
          <ServiceIcon service={svc} className="size-4 shrink-0 text-primary" />
          <span className="truncate">
            {project.name}/{svc.name}
          </span>
        </span>
      );
      body = (
        <>
          <div className="flex flex-wrap items-center gap-2">
            <StateBadge state={serviceState(svc)} />
            {svc.replicas > 1 && <span className="text-xs text-muted-foreground">{svc.replicas} replicas</span>}
          </div>
          {warning && <p className="text-sm text-destructive">{warning}</p>}
          <Field label="Image">
            <Mono>{svc.image}</Mono>
          </Field>
          {svc.domain && (
            <Field label="Domain">
              <a href={`https://${svc.domain}`} target="_blank" rel="noreferrer" className="flex items-center gap-1 hover:underline">
                <Globe className="size-3.5 text-sky-500" />
                {svc.domain}
              </a>
            </Field>
          )}
          {uses.length > 0 && <Field label="Uses">{uses.map((u) => u.name).join(", ")}</Field>}
          {joined.length > 0 && (
            <Field label="Networks">
              <span className="flex flex-wrap gap-1">
                {joined.map((n) => (
                  <span key={n.name} className="inline-flex items-center gap-1 rounded-full border border-violet-500/40 px-2 py-0.5 text-xs">
                    <Network className="size-3 text-violet-500" />
                    {n.custom}
                  </span>
                ))}
              </span>
              <span className="mt-1 block text-xs text-muted-foreground">
                Reached there as <Mono>{`${project.name}-${svc.name}`}</Mono>
              </span>
            </Field>
          )}
          {svc.containers.length > 0 && (
            <Field label="Containers">
              <Replicas nodes={svc.containers} networks={networks} />
            </Field>
          )}
          <Button asChild size="sm" variant="outline" className="w-full">
            <Link to={`/services/${svc.id}`}>
              Open service
              <ExternalLink data-icon="inline-end" />
            </Link>
          </Button>
        </>
      );
      break;
    }
    case "project":
      title = d.project.name;
      body = (
        <>
          <Field label="Network">
            <Mono>{d.net?.name ?? "—"}</Mono> {d.net?.subnet && <span className="text-muted-foreground">{d.net.subnet}</span>}
          </Field>
          <Field label="Services">{d.project.services.length}</Field>
          <Button asChild size="sm" variant="outline" className="w-full">
            <Link to={`/projects/${d.project.id}`}>
              Open project
              <ExternalLink data-icon="inline-end" />
            </Link>
          </Button>
        </>
      );
      break;
    case "network": {
      const members = server?.projects.flatMap((p) => p.services.filter((s) => s.networks.includes(d.net.customId!)).map((s) => `${p.name}-${s.name}`)) ?? [];
      title = d.net.custom;
      body = (
        <>
          <Field label="Docker network">
            <Mono>{d.net.name}</Mono> {d.net.subnet && <span className="text-muted-foreground">{d.net.subnet}</span>}
          </Field>
          <Field label="Services">{members.length ? members.map((m) => <Mono key={m}>{m} </Mono>) : "None yet"}</Field>
        </>
      );
      break;
    }
    case "server":
      title = d.server.name;
      body = (
        <>
          {d.server.error && <p className="text-sm text-destructive">{d.server.error}</p>}
          <Field label="Projects">{d.server.projects.length}</Field>
        </>
      );
      break;
    default: {
      const s = d.server;
      const container = d.kind === "traefik" ? s.proxy : d.kind === "tunnel" ? s.cloudflared : d.kind === "manager" ? s.manager?.container : undefined;
      title = { internet: "Internet", tunnel: "Cloudflare tunnel", traefik: "Traefik", manager: "kipitiny" }[d.kind];
      body = (
        <>
          {d.kind === "traefik" && (
            <Field label="Entrypoints">
              {s.entrypoints.map((ep) => (
                <span key={ep.name} className="block font-mono text-xs">
                  {ep.name} :{ep.port} {ep.hostPort ? `← host :${ep.hostPort}` : "(not published)"}
                  {ep.redirectTo && ` → ${ep.redirectTo}`}
                </span>
              ))}
            </Field>
          )}
          {d.kind === "manager" && (
            <Field label="Upstream">
              <Mono>{s.manager?.upstream}</Mono>
            </Field>
          )}
          {container && (
            <Field label="Container">
              <Replicas nodes={[container]} networks={networks} />
            </Field>
          )}
        </>
      );
    }
  }

  return (
    <aside className="absolute top-3 right-3 bottom-3 z-10 flex w-80 max-w-[calc(100%-1.5rem)] flex-col overflow-hidden rounded-xl border bg-card shadow-lg">
      <header className="flex items-center gap-2 border-b px-4 py-3 text-sm font-medium">
        <div className="min-w-0 flex-1 truncate">{title}</div>
        <Button variant="ghost" size="icon-sm" aria-label="Close" onClick={onClose}>
          <X />
        </Button>
      </header>
      <div className="flex-1 space-y-4 overflow-y-auto p-4 text-sm">{body}</div>
    </aside>
  );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="space-y-1">
      <p className="text-xs text-muted-foreground">{label}</p>
      <div className="break-all">{children}</div>
    </div>
  );
}
