import { useQuery } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, Cpu, MemoryStick, Network, type LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import { Empty, Section } from "@/components/common";
import { Card } from "@/components/ui/card";
import { formatBytes, formatCpu, formatRate } from "@/lib/format";
import { cn } from "@/lib/utils";
import { api, type ServiceUsage, type Usage } from "../api";

/** The manager samples every ten seconds; polling faster shows nothing new. */
const SAMPLE_MS = 10_000;

/** Current use of every running service, by service ID. */
export function useUsage() {
  return useQuery({ queryKey: ["usage"], queryFn: api.usage, refetchInterval: SAMPLE_MS });
}

/** Total memory of the services matching keep. */
export function memoryOf(usage: Record<string, ServiceUsage> | undefined, keep: (u: ServiceUsage) => boolean): number | undefined {
  if (!usage) return undefined;
  return Object.values(usage)
    .filter(keep)
    .reduce((n, u) => n + u.memoryBytes, 0);
}

/** A tiny line chart of values over time, scaled to max (or the largest value). */
export function Sparkline({ values, max, className }: { values: number[]; max?: number; className?: string }) {
  const w = 120;
  const h = 32;
  if (values.length < 2) return <div className={cn("h-8", className)} />;
  const top = Math.max(max ?? 0, ...values) || 1;
  const pts = values.map((v, i) => [(i / (values.length - 1)) * w, h - 1 - (v / top) * (h - 2)] as const);
  const line = pts.map(([x, y]) => `${x.toFixed(1)},${y.toFixed(1)}`).join(" ");
  return (
    <svg viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" aria-hidden className={cn("h-8 w-full overflow-visible", className)}>
      <polygon points={`0,${h} ${line} ${w},${h}`} className="fill-current opacity-10" />
      <polyline points={line} fill="none" stroke="currentColor" strokeWidth={1.5} vectorEffect="non-scaling-stroke" strokeLinejoin="round" />
    </svg>
  );
}

function UsageCard({ icon: Icon, label, value, hint, chart }: { icon: LucideIcon; label: string; value: ReactNode; hint?: ReactNode; chart: ReactNode }) {
  return (
    <Card size="sm" className="gap-1 px-3">
      <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <Icon className="size-3.5" />
        {label}
      </p>
      <p className="text-2xl font-semibold tabular-nums">{value}</p>
      <p className="truncate text-xs text-muted-foreground">{hint ?? " "}</p>
      <div className="mt-1 text-primary">{chart}</div>
    </Card>
  );
}

/** A service's stats, polled at the sampling rate; shared by every component that shows them. */
export function useServiceStats(serviceId: string) {
  return useQuery({ queryKey: ["service-stats", serviceId], queryFn: () => api.serviceStats(serviceId), refetchInterval: SAMPLE_MS });
}

/** CPU, memory and network cards for one point in time and its recent history. */
export function UsageGrid({ current: cur, history }: { current: Usage; history: Usage[] }) {
  return (
    <div className="grid gap-4 sm:grid-cols-3">
      <UsageCard
        icon={Cpu}
        label="CPU"
        value={formatCpu(cur.cpu)}
        hint={cur.cpu >= 100 ? `${(cur.cpu / 100).toFixed(1)} cores` : "of one core"}
        chart={<Sparkline values={history.map((p) => p.cpu)} max={100} />}
      />
      <UsageCard
        icon={MemoryStick}
        label="Memory"
        value={formatBytes(cur.memoryBytes)}
        hint={cur.memoryLimitBytes ? `of ${formatBytes(cur.memoryLimitBytes)} (${Math.round((cur.memoryBytes / cur.memoryLimitBytes) * 100)}%)` : "no limit"}
        chart={<Sparkline values={history.map((p) => p.memoryBytes)} max={cur.memoryLimitBytes} />}
      />
      <UsageCard
        icon={Network}
        label="Network"
        value={formatRate(cur.netRx + cur.netTx)}
        hint={
          <span className="inline-flex items-center gap-2">
            <span className="inline-flex items-center gap-0.5">
              <ArrowDown className="size-3" aria-label="received" />
              {formatRate(cur.netRx)}
            </span>
            <span className="inline-flex items-center gap-0.5">
              <ArrowUp className="size-3" aria-label="sent" />
              {formatRate(cur.netTx)}
            </span>
          </span>
        }
        chart={<Sparkline values={history.map((p) => p.netRx + p.netTx)} />}
      />
    </div>
  );
}

/** Live CPU, memory and network use of a service, summed over its replicas, with the last five minutes. */
export function ServiceUsageSection({ serviceId }: { serviceId: string }) {
  const stats = useServiceStats(serviceId);
  const cur = stats.data?.current;
  const replicas = cur?.replicas ?? 0;
  return (
    <Section
      title="Usage"
      description={`${replicas > 1 ? `Total of ${replicas} replicas; open a container below for its own share.` : "Of the running replica."} The charts cover the last five minutes.`}
    >
      {stats.isPending ? null : !cur ? <Empty>Not running.</Empty> : <UsageGrid current={cur} history={stats.data?.history ?? []} />}
    </Section>
  );
}
