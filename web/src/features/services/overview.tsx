import { useMutation } from "@tanstack/react-query";
import { ChevronRight } from "lucide-react";
import { useState } from "react";
import { Mono, SecretList, StateBadge } from "@/components/common";
import { Section } from "@kipit-labs/kipitidy/section";
import { Button } from "@kipit-labs/kipitidy/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@kipit-labs/kipitidy/collapsible";
import { UsageGrid, useServiceStats } from "@/features/services/usage";
import { formatBytes, formatCpu } from "@/lib/format";
import { cn } from "@/lib/utils";
import { api, databasePorts, databaseUrlKey, type Connection, type Container as ContainerT, type DatabaseKind } from "../../api";

/** One container; opening it shows its own share of the service's usage. */
export function ContainerRow({ serviceId, container: c }: { serviceId: string; container: ContainerT }) {
  const stats = useServiceStats(serviceId).data?.containers[c.id];
  const cur = stats?.current;
  return (
    <Collapsible asChild>
      <li className={cn("data-[state=open]:bg-muted/30", c.retired && "opacity-50")}>
        <CollapsibleTrigger className="group flex w-full flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-left outline-none focus-visible:bg-muted/50">
          <ChevronRight className="size-4 shrink-0 text-muted-foreground transition-transform group-data-[state=open]:rotate-90" />
          <span className="font-mono text-xs">{c.name}</span>
          <StateBadge state={c.health ?? c.state} />
          {cur && (
            <span className="text-xs text-muted-foreground tabular-nums">
              {formatCpu(cur.cpu)} · {formatBytes(cur.memoryBytes)}
            </span>
          )}
          <span className="flex-1 text-right text-xs text-muted-foreground">
            {c.retired ? "previous deploy, kept for its logs" : c.status}
          </span>
        </CollapsibleTrigger>
        <CollapsibleContent>
          <div className="px-3 pt-1 pb-3">
            {cur ? (
              <UsageGrid current={cur} history={stats.history} />
            ) : (
              <p className="text-sm text-muted-foreground">{c.state === "running" ? "No usage sampled yet." : "Not running."}</p>
            )}
          </div>
        </CollapsibleContent>
      </li>
    </Collapsible>
  );
}

/** The database's address inside the project; its credentials on demand. */
export function ConnectionCard({ serviceId, host, kind }: { serviceId: string; host: string; kind: DatabaseKind }) {
  const [conn, setConn] = useState<Connection | null>(null);
  const reveal = useMutation({ meta: { error: "Couldn't show the connection details" }, mutationFn: () => api.connection(serviceId), onSuccess: setConn });
  return (
    <Section
      title="Connection"
      description={
        <>
          Reachable only inside the project at <Mono>{`${host}:${databasePorts[kind]}`}</Mono>. Apps use it from their
          Environment (Secrets → Connect database), e.g.{" "}
          <Mono>{`${databaseUrlKey(kind)}={{ db.${host}.URL }}`}</Mono>.
        </>
      }
      actions={
        <Button variant="outline" size="sm" onClick={() => (conn ? setConn(null) : reveal.mutate())} disabled={reveal.isPending}>
          {conn ? "Hide" : "Reveal credentials"}
        </Button>
      }
    >
      {conn && (
        <SecretList
          rows={[
            ["Host", `${conn.host}:${conn.port}`],
            ...(conn.database ? [["Database", conn.database] as [string, string]] : []),
            ["User", conn.user],
            ["Password", conn.password],
            ["URL", conn.url],
          ]}
        />
      )}
    </Section>
  );
}
