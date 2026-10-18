import { useQuery } from "@tanstack/react-query";
import { Activity, Clock, Eye, LogOut, MousePointerClick, Users, X } from "lucide-react";
import { useState } from "react";
import { CopyField, Empty, ErrorText, Loading, Mono, Section, StatCard } from "@/components/common";
import { Button } from "@/components/ui/button";
import { Segmented, SegmentedItem } from "@/components/ui/segmented";
import { cn } from "@/lib/utils";
import { countryLabel, duration, Visits } from "./analytics-visits";
import { api, type AnalyticsRow, type AnalyticsStats, type Service } from "../api";

const HOUR = 3600;
const DAY = 86400;

const ranges = {
  "24h": { label: "24 hours", span: DAY, interval: HOUR },
  "7d": { label: "7 days", span: 7 * DAY, interval: DAY },
  "30d": { label: "30 days", span: 30 * DAY, interval: DAY },
  "90d": { label: "90 days", span: 90 * DAY, interval: DAY },
  "12m": { label: "12 months", span: 365 * DAY, interval: DAY },
} as const;
type Range = keyof typeof ranges;

/** Breakdown cards, in the order they're shown; titles name the column. */
const breakdowns: { key: string; title: string; column: string }[] = [
  { key: "pages", title: "Pages", column: "Path" },
  { key: "entries", title: "Entry pages", column: "Path" },
  { key: "exits", title: "Exit pages", column: "Path" },
  { key: "referrers", title: "Referrers", column: "Site" },
  { key: "countries", title: "Countries", column: "Country" },
  { key: "languages", title: "Languages", column: "Language" },
  { key: "browsers", title: "Browsers", column: "Browser" },
  { key: "os", title: "Operating systems", column: "OS" },
  { key: "devices", title: "Devices", column: "Device" },
  { key: "screens", title: "Screens", column: "Size" },
  { key: "utm_source", title: "UTM source", column: "Source" },
  { key: "utm_medium", title: "UTM medium", column: "Medium" },
  { key: "utm_campaign", title: "UTM campaign", column: "Campaign" },
  { key: "queries", title: "Query parameters", column: "Keys" },
  { key: "events", title: "Events", column: "Name" },
  { key: "identities", title: "Identified users", column: "Identity" },
  { key: "props", title: "Event properties", column: "Event: property" },
];

/** Cookieless visits of an app's domain, from the analytics server. */
export function AnalyticsTab({ svc }: { svc: Service }) {
  const [range, setRange] = useState<Range>("7d");
  // "<breakdown>:<value>", as the API takes them.
  const [filters, setFilters] = useState<string[]>([]);
  const r = ranges[range];
  const addFilter = (f: string) => !filters.includes(f) && setFilters([...filters, f]);
  const stats = useQuery({
    queryKey: ["service-analytics", svc.id, range, filters],
    queryFn: () => {
      const to = Math.ceil(Date.now() / 1000 / 60) * 60;
      return api.serviceAnalytics(svc.id, { from: to - r.span, to, interval: r.interval, offset: -new Date().getTimezoneOffset() * 60, filters });
    },
    placeholderData: (prev) => prev,
    refetchInterval: 60_000,
  });

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-sm text-muted-foreground">
          Visits of <Mono>{svc.domain}</Mono>, counted without cookies.
        </p>
        <Segmented size="sm" value={range} onValueChange={(v) => setRange(v as Range)} aria-label="Range">
          {Object.entries(ranges).map(([k, v]) => (
            <SegmentedItem key={k} value={k}>
              {v.label}
            </SegmentedItem>
          ))}
        </Segmented>
      </div>
      {filters.length > 0 && (
        <div className="flex flex-wrap items-center gap-2">
          {filters.map((f) => (
            <FilterChip key={f} filter={f} onRemove={() => setFilters(filters.filter((x) => x !== f))} />
          ))}
          {filters.length > 1 && (
            <Button variant="ghost" size="sm" onClick={() => setFilters([])}>
              Clear filters
            </Button>
          )}
        </div>
      )}
      {stats.isPending ? (
        <Loading />
      ) : stats.error ? (
        <ErrorText error={stats.error} />
      ) : (
        <Stats
          stats={stats.data}
          span={r.span}
          interval={r.interval}
          filtered={filters.length > 0}
          onFilter={addFilter}
        />
      )}
      <Visits svc={svc} span={r.span} filters={filters} onFilter={addFilter} />
    </div>
  );
}

function FilterChip({ filter, onRemove }: { filter: string; onRemove: () => void }) {
  const i = filter.indexOf(":");
  const key = filter.slice(0, i);
  const value = filter.slice(i + 1);
  const b = breakdowns.find((b) => b.key === key);
  return (
    <span className="inline-flex items-center gap-1 rounded-md border bg-muted/40 py-0.5 pr-0.5 pl-2 text-sm">
      <span className="text-muted-foreground">{b?.title ?? key}:</span>
      <span className="max-w-64 truncate">{key === "countries" ? countryLabel(value) : value}</span>
      <Button variant="ghost" size="icon-xs" aria-label={`Remove filter ${b?.title ?? key}`} onClick={onRemove}>
        <X />
      </Button>
    </span>
  );
}

function Stats({
  stats,
  span,
  interval,
  filtered,
  onFilter,
}: {
  stats: AnalyticsStats;
  span: number;
  interval: number;
  filtered: boolean;
  onFilter: (filter: string) => void;
}) {
  const bounce = stats.visits ? Math.round((stats.bounces / stats.visits) * 100) : 0;
  return (
    <>
      <div className="grid grid-cols-2 gap-4 md:grid-cols-3 xl:grid-cols-6">
        <StatCard icon={Users} label="Visitors" value={stats.visitors} hint="Unique per day" />
        <StatCard icon={MousePointerClick} label="Visits" value={stats.visits} hint="30 min idle ends one" />
        <StatCard icon={Eye} label="Pageviews" value={stats.pageviews} />
        <StatCard icon={LogOut} label="Bounce rate" value={stats.visits ? `${bounce}%` : "—"} hint="One-page visits" />
        <StatCard icon={Clock} label="Visit duration" value={stats.visits ? duration(stats.duration / stats.visits) : "—"} hint="Average" />
        <StatCard icon={Activity} label="Active" value={stats.active} hint="Last 30 minutes" tone={stats.active > 0 ? "good" : undefined} />
      </div>
      <Section title="Traffic">
        <Chart series={stats.series} span={span} interval={interval} />
      </Section>
      {!filtered && stats.pageviews === 0 && stats.visits === 0 && (
        <Section title="Start counting" description="No visit in this range yet. Add the script to the app's pages, e.g. in the <head>:">
          <CopyField value={stats.script} />
          <p className="text-sm text-muted-foreground">
            Custom events: <Mono>kipitiny.track("signup", {"{ plan: \"pro\" }"})</Mono>, or a <Mono>data-kipitiny-event="signup"</Mono>{" "}
            attribute on what gets clicked (its <Mono>data-kipitiny-event-*</Mono> attributes become properties).
          </p>
        </Section>
      )}
      <div className="grid gap-4 lg:grid-cols-2">
        {breakdowns
          .filter((b) => (stats.breakdowns[b.key]?.length ?? 0) > 0)
          .map((b) => (
            <Breakdown
              key={b.key}
              title={b.title}
              column={b.column}
              rows={stats.breakdowns[b.key]}
              label={b.key === "countries" ? countryLabel : undefined}
              // Props mix the event and the property: not one column to filter on.
              onPick={b.key === "props" ? undefined : (v) => onFilter(`${b.key}:${v}`)}
            />
          ))}
      </div>
      {stats.pageviews + stats.visits > 0 && (
        <Section title="Tracking script" description="On every page that should be counted.">
          <CopyField value={stats.script} />
        </Section>
      )}
    </>
  );
}

/** Pageviews and visitors per bucket, missing buckets as zero. */
function Chart({ series, span, interval }: { series: AnalyticsStats["series"]; span: number; interval: number }) {
  const [hover, setHover] = useState<number | null>(null);
  const byT = new Map(series.map((p) => [p.t, p]));
  const offset = -new Date().getTimezoneOffset() * 60;
  const now = Math.floor(Date.now() / 1000);
  const last = Math.floor((now + offset) / interval) * interval - offset;
  const n = Math.round(span / interval);
  const points = Array.from({ length: n }, (_, i) => {
    const t = last - (n - 1 - i) * interval;
    return byT.get(t) ?? { t, pageviews: 0, visitors: 0 };
  });
  const top = Math.max(1, ...points.map((p) => p.pageviews));
  const fmt = (t: number) =>
    new Date(t * 1000).toLocaleString(undefined, interval < DAY ? { hour: "2-digit", minute: "2-digit" } : { month: "short", day: "numeric" });
  if (series.length === 0) return <Empty>No visits in this range.</Empty>;
  const h = hover === null ? null : points[hover];
  return (
    <div className="space-y-2">
      <div className="flex h-5 items-center gap-4 text-xs text-muted-foreground">
        <span className="flex items-center gap-1.5">
          <span className="size-2.5 rounded-sm bg-primary/25" /> Pageviews
        </span>
        <span className="flex items-center gap-1.5">
          <span className="size-2.5 rounded-sm bg-primary" /> Visitors
        </span>
        {h && (
          <span className="ml-auto text-foreground tabular-nums">
            {fmt(h.t)} · {h.pageviews} pageviews · {h.visitors} visitors
          </span>
        )}
      </div>
      <div className="flex h-40 items-end gap-px" role="img" aria-label="Pageviews and visitors over time" onPointerLeave={() => setHover(null)}>
        {points.map((p, i) => (
          <div
            key={p.t}
            className={cn("relative flex h-full flex-1 items-end rounded-t-sm", hover === i && "bg-muted")}
            onPointerEnter={() => setHover(i)}
          >
            <div className="absolute inset-x-0 bottom-0 rounded-t-sm bg-primary/25" style={{ height: `${(p.pageviews / top) * 100}%` }} />
            <div className="absolute inset-x-[20%] bottom-0 rounded-t-sm bg-primary" style={{ height: `${(p.visitors / top) * 100}%` }} />
          </div>
        ))}
      </div>
      <div className="flex justify-between text-xs text-muted-foreground tabular-nums">
        <span>{fmt(points[0].t)}</span>
        <span>{fmt(points[points.length - 1].t)}</span>
      </div>
    </div>
  );
}

function Breakdown({
  title,
  column,
  rows,
  label,
  onPick,
}: {
  title: string;
  column: string;
  rows: AnalyticsRow[];
  label?: (v: string) => string;
  onPick?: (value: string) => void;
}) {
  const top = Math.max(1, ...rows.map((r) => r.count));
  return (
    <Section title={title}>
      <table className="w-full table-fixed text-sm">
        <thead className="text-xs text-muted-foreground">
          <tr>
            <th className="pb-1.5 text-left font-normal">{column}</th>
            <th className="w-20 pb-1.5 text-right font-normal">Visitors</th>
            <th className="w-20 pb-1.5 text-right font-normal">Count</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.value}>
              <td className="relative py-1 pr-2">
                <span className="absolute inset-y-0.5 left-0 rounded-sm bg-primary/10" style={{ width: `${(r.count / top) * 100}%` }} />
                {onPick ? (
                  <button
                    type="button"
                    className="relative block w-full truncate rounded-sm px-1.5 text-left hover:underline focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
                    title={`Filter by ${r.value}`}
                    onClick={() => onPick(r.value)}
                  >
                    {label ? label(r.value) : r.value}
                  </button>
                ) : (
                  <span className="relative block truncate px-1.5" title={r.value}>
                    {label ? label(r.value) : r.value}
                  </span>
                )}
              </td>
              <td className="py-1 text-right tabular-nums">{r.visitors}</td>
              <td className="py-1 text-right text-muted-foreground tabular-nums">{r.count}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </Section>
  );
}
