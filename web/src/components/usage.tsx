import { useQuery } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, Cpu, HardDrive, MemoryStick, Network, type LucideIcon } from "lucide-react";
import { useState, type PointerEvent, type ReactNode } from "react";
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

/** A tiny line chart of values over time, scaled to max (or the largest value). With format, hovering shows the value under the pointer. */
export function Sparkline({
  values,
  max,
  format,
  times,
  className,
}: {
  values: number[];
  max?: number;
  format?: (v: number) => string;
  /** ISO time of each value, shown next to it on hover. */
  times?: string[];
  className?: string;
}) {
  const [hover, setHover] = useState<number | null>(null);
  const w = 120;
  const h = 32;
  if (values.length < 2) return <div className={cn("h-8", className)} />;
  const top = Math.max(max ?? 0, ...values) || 1;
  const pts = values.map((v, i) => [(i / (values.length - 1)) * w, h - 1 - (v / top) * (h - 2)] as const);
  const line = pts.map(([x, y]) => `${x.toFixed(1)},${y.toFixed(1)}`).join(" ");
  const onMove = (e: PointerEvent<HTMLDivElement>) => {
    const r = e.currentTarget.getBoundingClientRect();
    const i = Math.round(((e.clientX - r.left) / r.width) * (values.length - 1));
    setHover(Math.min(values.length - 1, Math.max(0, i)));
  };
  const at = hover === null ? null : { x: (pts[hover][0] / w) * 100, y: (pts[hover][1] / h) * 100 };
  return (
    <div
      className={cn("relative h-8 w-full", className)}
      onPointerMove={format && onMove}
      onPointerLeave={() => setHover(null)}
    >
      <svg viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" aria-hidden className="size-full overflow-visible">
        <polygon points={`0,${h} ${line} ${w},${h}`} className="fill-current opacity-10" />
        <polyline points={line} fill="none" stroke="currentColor" strokeWidth={1.5} vectorEffect="non-scaling-stroke" strokeLinejoin="round" />
      </svg>
      {format && hover !== null && at && (
        <>
          <span className="pointer-events-none absolute inset-y-0 w-px bg-current opacity-30" style={{ left: `${at.x}%` }} />
          <span
            className="pointer-events-none absolute size-2 -translate-1/2 rounded-full border-2 border-background bg-current"
            style={{ left: `${at.x}%`, top: `${at.y}%` }}
          />
          <span
            className={cn(
              "pointer-events-none absolute bottom-full z-10 mb-1.5 rounded-md bg-foreground px-2 py-1 text-xs whitespace-nowrap text-background tabular-nums",
              at.x < 20 ? "" : at.x > 80 ? "-translate-x-full" : "-translate-x-1/2",
            )}
            style={{ left: `${at.x}%` }}
          >
            <span className="font-medium">{format(values[hover])}</span>
            {times?.[hover] && <span className="opacity-70"> · {new Date(times[hover]).toLocaleTimeString()}</span>}
          </span>
        </>
      )}
    </div>
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
  const times = history.map((p) => p.at);
  return (
    <div className="grid gap-4 sm:grid-cols-3">
      <UsageCard
        icon={Cpu}
        label="CPU"
        value={formatCpu(cur.cpu)}
        hint={cur.cpu >= 100 ? `${(cur.cpu / 100).toFixed(1)} cores` : "of one core"}
        chart={<Sparkline values={history.map((p) => p.cpu)} max={100} format={formatCpu} times={times} />}
      />
      <UsageCard
        icon={MemoryStick}
        label="Memory"
        value={formatBytes(cur.memoryBytes)}
        hint={cur.memoryLimitBytes ? `of ${formatBytes(cur.memoryLimitBytes)} (${Math.round((cur.memoryBytes / cur.memoryLimitBytes) * 100)}%)` : "no limit"}
        chart={<Sparkline values={history.map((p) => p.memoryBytes)} max={cur.memoryLimitBytes} format={formatBytes} times={times} />}
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
        chart={<Sparkline values={history.map((p) => p.netRx + p.netTx)} format={formatRate} times={times} />}
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

const percent = (n: number, of: number) => (of ? `${Math.round((n / of) * 100)}%` : "");

/** How long a host has been up, in its largest unit. */
function formatUptime(sec: number): string {
  const d = Math.floor(sec / 86400);
  return d > 0 ? `${d} day${d > 1 ? "s" : ""}` : `${Math.floor(sec / 3600)} h`;
}

/** Live CPU, memory and disk use of a server's host, with the last five minutes; containers is the memory its services use. */
export function HostUsageSection({ serverId, containers }: { serverId: string; containers?: number }) {
  const stats = useQuery({ queryKey: ["server-stats", serverId], queryFn: () => api.serverStats(serverId), refetchInterval: SAMPLE_MS });
  const cur = stats.data?.current;
  const history = stats.data?.history ?? [];
  const times = history.map((p) => p.at);
  return (
    <Section title="Host" description="The whole machine, not only kipitiny's containers. The charts cover the last five minutes.">
      {stats.isPending ? null : !cur ? (
        <Empty>Can't read the host right now.</Empty>
      ) : (
        <div className="grid gap-4 sm:grid-cols-3">
          <UsageCard
            icon={Cpu}
            label="CPU"
            value={formatCpu(cur.cpu)}
            hint={`${cur.cores} cores · load ${cur.load.map((l) => l.toFixed(2)).join(" ")} · up ${formatUptime(cur.uptimeSec)}`}
            chart={<Sparkline values={history.map((p) => p.cpu)} max={100} format={formatCpu} times={times} />}
          />
          <UsageCard
            icon={MemoryStick}
            label="Memory"
            value={percent(cur.memoryBytes, cur.memoryTotalBytes)}
            hint={[
              `${formatBytes(cur.memoryBytes)} of ${formatBytes(cur.memoryTotalBytes)}`,
              containers !== undefined && `containers ${formatBytes(containers)}`,
              cur.swapTotalBytes > 0 && `swap ${formatBytes(cur.swapBytes)}`,
            ]
              .filter(Boolean)
              .join(" · ")}
            chart={<Sparkline values={history.map((p) => p.memoryBytes)} max={cur.memoryTotalBytes} format={formatBytes} times={times} />}
          />
          <UsageCard
            icon={HardDrive}
            label="Disk"
            value={percent(cur.diskBytes, cur.diskTotalBytes)}
            hint={`${formatBytes(cur.diskBytes)} of ${formatBytes(cur.diskTotalBytes)}, Docker's data`}
            chart={<Sparkline values={history.map((p) => p.diskBytes)} max={cur.diskTotalBytes} format={formatBytes} times={times} />}
          />
        </div>
      )}
    </Section>
  );
}
