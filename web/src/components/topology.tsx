import { stateColors } from "@/components/common";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { liveState, troubled } from "@/lib/format";
import { cn } from "@/lib/utils";
import { isDatabase, type TopoNetwork, type TopoNode, type TopoProject, type TopoService } from "../api";

export const PROXY_NETWORK = "kipitiny-proxy";

/** Why a service is misconfigured on the map, or "". */
export function serviceWarning(svc: TopoService): string {
  const live = svc.containers.filter((c) => !c.retired);
  const onProxy = live.some((c) => c.endpoints.some((e) => e.network === PROXY_NETWORK));
  if (isDatabase(svc.kind) && onProxy) return "Attached to the proxy network";
  if (svc.domain && live.length > 0 && !onProxy) return "Not on the proxy network: Traefik can't reach it";
  return "";
}

export const serviceState = (svc: TopoService) => liveState({ stopped: !!svc.stopped, containers: svc.containers });

/** How many of the project's services are misconfigured or not running well. */
export function projectProblems(p: TopoProject): number {
  return p.services.filter((svc) => serviceWarning(svc) || troubled(serviceState(svc))).length;
}

/** One pill per container; the tooltip lists its networks (by name for the ones created by hand) and addresses. */
export function Replicas({ nodes, networks = [] }: { nodes: TopoNode[]; networks?: TopoNetwork[] }) {
  const custom = new Map(networks.filter((n) => n.custom).map((n) => [n.name, n.custom!]));
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
                {custom.get(e.network) ?? e.network} {e.ip ?? "no address"}
                {e.aliases?.length ? ` (${e.aliases.join(", ")})` : ""}
              </p>
            ))}
          </TooltipContent>
        </Tooltip>
      ))}
    </div>
  );
}
