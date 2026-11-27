import { TriangleAlert } from "lucide-react";
import { Link } from "react-router";
import { ServiceIconTile } from "@/components/service-icon";
import { StateBadge, Tag } from "@/components/common";
import { TagChips } from "@/components/tags";
import { useUsage } from "@/features/services/usage";
import { Card } from "@kipit-labs/kipitidy/card";
import { formatBytes, liveState } from "@/lib/format";
import { isDatabase, type Service as ServiceT } from "../../api";
import { ReachIcon, reachOf } from "@/features/services/reach";
import { issueRing, issueText, serviceIssue } from "@/features/map/topology";
import { cn } from "@/lib/utils";

export function ServiceGroup({ title, services }: { title: string; services: ServiceT[] }) {
  if (services.length === 0) return null;
  return (
    <section className="space-y-2">
      <h2 className="text-xs font-medium tracking-wide text-muted-foreground uppercase">{title}</h2>
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {services.map((s) => (
          <ServiceCard key={s.id} svc={s} />
        ))}
      </div>
    </section>
  );
}

function ServiceCard({ svc: s }: { svc: ServiceT }) {
  const isDb = isDatabase(s.kind);
  const live = s.containers.filter((c) => !c.retired);
  const used = useUsage().data?.[s.id]?.memoryBytes;
  const problem = serviceIssue(s);
  return (
    <Link to={`/services/${s.id}`} className="group rounded-xl outline-none focus-visible:ring-3 focus-visible:ring-ring/50">
      <Card
        className={cn(
          "h-full gap-3 px-4 transition-all group-hover:-translate-y-px group-hover:shadow-md group-hover:ring-foreground/20",
          problem && issueRing[problem.tone],
        )}
      >
        <div className="flex items-start gap-3">
          <ServiceIconTile service={s} />
          <div className="min-w-0 flex-1">
            <p className="truncate font-medium">{s.name}</p>
            <p className="truncate font-mono text-xs text-muted-foreground">
              {s.image}
            </p>
          </div>
          <StateBadge state={liveState(s)} />
        </div>
        {problem && (
          <p title={problem.text} className={cn("flex items-center gap-1.5 text-xs", issueText[problem.tone])}>
            <TriangleAlert className="size-3.5 shrink-0" />
            <span className="truncate">{problem.short}</span>
          </p>
        )}
        {/* The first of what's pinned to the bottom takes the space. */}
        <TagChips tags={s.tags} className="mt-auto" />
        <div className={cn("flex flex-wrap items-center gap-1.5", !s.tags?.length && "mt-auto")}>
          {isDb ? (
            <>
              <Tag>
                {used !== undefined && `${formatBytes(used)} / `}
                {s.memoryMb} MB
              </Tag>
              {s.cpus > 0 && <Tag>{s.cpus} CPU</Tag>}
            </>
          ) : (
            <>
              <Tag className="flex items-center gap-1 font-mono font-normal">
                <ReachIcon service={s} className="size-3" />
                {reachOf(s).label}
              </Tag>
              <Tag>
                {live.filter((c) => c.state === "running").length}/{s.replicas} replica{s.replicas > 1 ? "s" : ""}
              </Tag>
              {used !== undefined && <Tag>{formatBytes(used)} memory</Tag>}
            </>
          )}
        </div>
      </Card>
    </Link>
  );
}
