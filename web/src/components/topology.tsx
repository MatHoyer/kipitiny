import { stateColors } from "@/components/common";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { liveState, troubled } from "@/lib/format";
import { cn } from "@/lib/utils";
import { isDatabase, type Container, type TopoNetwork, type TopoNode, type TopoProject, type TopoService } from "../api";

export const PROXY_NETWORK = "kipitiny-proxy";

export type ServiceIssue = {
  tone: "error" | "warn";
  /** Fits a map node or a card. */
  short: string;
  text: string;
};

type IssueSource = Pick<TopoService, "kind" | "domain" | "dns" | "routePending" | "networkIssues"> & {
  containers: (Container & { endpoints?: TopoNode["endpoints"] })[];
};

/** What's wrong with a service, worst first: the map and the service cards show the same. */
export function serviceIssue(svc: IssueSource): ServiceIssue | null {
  const live = svc.containers.filter((c) => !c.retired);
  // The networks its running containers miss, from the manager (cards and map alike), e.g. one removed by hand.
  const missing = svc.networkIssues ?? [];
  if (missing.length > 0) {
    return { tone: "error", short: capitalize(missing[0]), text: `${missing.map(capitalize).join(". ")}. kipitiny attaches it again within a minute.` };
  }
  // Only the topology knows the containers' networks.
  if (live.length > 0 && live.every((c) => c.endpoints)) {
    const onProxy = live.some((c) => c.endpoints!.some((e) => e.network === PROXY_NETWORK));
    if (isDatabase(svc.kind) && onProxy) return issue("error", "Attached to the proxy network");
    if (svc.domain && !onProxy) return issue("error", "Not on the proxy network: Traefik can't reach it");
  }
  const dns = svc.dns;
  if (dns && (dns.state === "error" || dns.state === "conflict")) return { tone: "error", short: `DNS ${dns.state}`, text: dns.message ?? "" };
  if (dns?.state === "warning") return { tone: "warn", short: "DNS warning", text: dns.message ?? "" };
  if (svc.routePending) return issue("warn", "Domain changed: deploy to serve it");
  return null;
}

const issue = (tone: ServiceIssue["tone"], text: string): ServiceIssue => ({ tone, short: text, text });
const capitalize = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

export const issueRing = { error: "ring-destructive/60", warn: "ring-amber-500/60" } as const;
export const issueText = { error: "text-destructive", warn: "text-amber-600 dark:text-amber-400" } as const;

export const serviceState = (svc: TopoService) => liveState({ stopped: !!svc.stopped, containers: svc.containers });

/** How many of the project's services are misconfigured or not running well. */
export function projectProblems(p: TopoProject): number {
  return p.services.filter((svc) => serviceIssue(svc)?.tone === "error" || troubled(serviceState(svc))).length;
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
