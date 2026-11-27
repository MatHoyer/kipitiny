import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useSearchParams } from "react-router";
import { Empty, ErrorText, Mono } from "@/components/common";
import { CopyField } from "@kipit-labs/kipitidy/copy-button";
import { Loading } from "@kipit-labs/kipitidy/loading";
import { Section } from "@kipit-labs/kipitidy/section";
import { ConfirmDialog } from "@kipit-labs/kipitidy/confirm-dialog";
import { Button } from "@kipit-labs/kipitidy/button";
import { Input } from "@kipit-labs/kipitidy/input";
import { Segmented, SegmentedItem } from "@kipit-labs/kipitidy/segmented";
import { timeAgo } from "@/lib/format";
import { cn } from "@/lib/utils";
import { api, type IssueStatus, type Service } from "../../api";
import { IssueBadge, IssueSheet } from "@/features/services/issue-sheet";

const HOUR = 3600;
const DAY = 86400;
const ago = (t: number) => timeAgo(new Date(t * 1000).toISOString());

const ranges = {
  "24h": { label: "24 hours", span: DAY, interval: HOUR },
  "7d": { label: "7 days", span: 7 * DAY, interval: 3 * HOUR },
  "30d": { label: "30 days", span: 30 * DAY, interval: DAY },
} as const;
type Range = keyof typeof ranges;

const sorts = { lastSeen: "Last seen", events: "Events", users: "Users", firstSeen: "Newest" } as const;

/** Errors the app's Sentry SDK reported, grouped into issues. canChange resolves and ignores them. */
export function ErrorsTab({ svc, canChange }: { svc: Service; canChange: boolean }) {
  const [params, setParams] = useSearchParams();
  const [status, setStatus] = useState<IssueStatus>("open");
  const [sort, setSort] = useState<keyof typeof sorts>("lastSeen");
  const [search, setSearch] = useState("");
  const list = useQuery({
    queryKey: ["service-errors", svc.id, status, sort, search],
    queryFn: () => api.serviceErrors(svc.id, { status, sort, q: search }),
    placeholderData: keepPreviousData,
    refetchInterval: 15_000,
  });
  const issue = Number(params.get("issue")) || null;
  const open = (id: number | null) =>
    setParams(
      (p) => {
        if (id) p.set("issue", String(id));
        else p.delete("issue");
        return p;
      },
      { replace: true },
    );
  if (list.isPending) return <Loading />;
  if (list.error) return <ErrorText error={list.error} />;
  const { issues, counts, dsn } = list.data;
  const none = counts.open + counts.resolved + counts.ignored === 0 && !search;
  return (
    <div className="flex flex-col gap-6">
      {none ? (
        <Setup dsn={dsn} />
      ) : (
        <Section plain>
          <Series svc={svc} />
        </Section>
      )}
      {!none && (
        <Section
          title="Issues"
          description="The same error, from any replica or release, is one issue. Resolve it once the fix is deployed: it reopens if it happens again."
        >
          <div className="flex flex-wrap items-center gap-2">
            <Segmented size="sm" value={status} onValueChange={(v) => setStatus(v as IssueStatus)} aria-label="Status">
              {(["open", "resolved", "ignored"] as const).map((s) => (
                <SegmentedItem key={s} value={s}>
                  {s[0].toUpperCase() + s.slice(1)} <span className="tabular-nums text-muted-foreground">{counts[s]}</span>
                </SegmentedItem>
              ))}
            </Segmented>
            <Input
              id="errors-search"
              className="h-8 max-w-64 min-w-40 flex-1"
              placeholder="Search title or file"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
            <Segmented size="sm" className="ml-auto" value={sort} onValueChange={(v) => setSort(v as keyof typeof sorts)} aria-label="Sort">
              {Object.entries(sorts).map(([k, label]) => (
                <SegmentedItem key={k} value={k}>
                  {label}
                </SegmentedItem>
              ))}
            </Segmented>
          </div>
          {issues.length === 0 ? (
            <Empty>{search ? "No issue matches." : `No ${status} issues.`}</Empty>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead className="text-xs text-muted-foreground">
                  <tr className="text-left">
                    <th className="pb-1.5 font-normal">Issue</th>
                    <th className="w-28 pb-1.5 font-normal">Last 24 h</th>
                    <th className="pb-1.5 text-right font-normal">Events</th>
                    <th className="pb-1.5 text-right font-normal">Users</th>
                    <th className="pb-1.5 pl-3 font-normal">Last seen</th>
                  </tr>
                </thead>
                <tbody>
                  {issues.map((is) => (
                    <tr key={is.id} className="cursor-pointer border-t hover:bg-muted/40" onClick={() => open(is.id)}>
                      <td className="max-w-0 min-w-64 py-2 pr-3">
                        <button
                          type="button"
                          className="block w-full truncate text-left font-medium hover:underline focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
                        >
                          {is.title}
                        </button>
                        <span className="flex items-center gap-2 text-xs text-muted-foreground">
                          <IssueBadge issue={is} />
                          <span className="truncate font-mono">{is.culprit}</span>
                        </span>
                      </td>
                      <td className="py-2 pr-3">
                        <Spark values={is.spark ?? []} tone={is.regressedAt && is.status === "open" ? "warn" : "bad"} />
                      </td>
                      <td className="py-2 text-right tabular-nums">{is.events.toLocaleString()}</td>
                      <td className="py-2 text-right tabular-nums">{is.users.toLocaleString()}</td>
                      <td className="py-2 pl-3 whitespace-nowrap text-muted-foreground">{ago(is.lastSeen)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Section>
      )}
      {!none && (
        <Section title="Connection" description="The address the app's Sentry SDK reports to. Each deploy sets it as SENTRY_DSN.">
          <CopyField value={dsn} />
        </Section>
      )}
      <IssueSheet svc={svc} id={issue} canChange={canChange} onClose={() => open(null)} />
    </div>
  );
}

/** What to do before the first error comes in. */
function Setup({ dsn }: { dsn: string }) {
  return (
    <Section
      title="No errors yet"
      description="Errors show up here once the app reports them with a Sentry SDK. Each deploy sets SENTRY_DSN, SENTRY_RELEASE and SENTRY_ENVIRONMENT, which server SDKs read by themselves."
    >
      <CopyField value={dsn} />
      <pre className="overflow-x-auto rounded-md bg-muted p-3 font-mono text-xs leading-relaxed">
        {`# Python: pip install sentry-sdk
import sentry_sdk
sentry_sdk.init()

// Node: npm i @sentry/node
import * as Sentry from "@sentry/node";
Sentry.init({ dsn: process.env.SENTRY_DSN });`}
      </pre>
      <p className="text-sm text-muted-foreground">A browser bundle needs the DSN above in its Sentry.init. Redeploy the app if it ran before error tracking was on.</p>
    </Section>
  );
}

function Spark({ values, tone }: { values: number[]; tone: "bad" | "warn" }) {
  const top = Math.max(1, ...values);
  const pts = values.map((v, i) => `${(i / Math.max(1, values.length - 1)) * 100},${20 - (v / top) * 18}`).join(" ");
  return (
    <svg viewBox="0 0 100 22" preserveAspectRatio="none" className="h-5 w-24" aria-label={`${values.reduce((a, b) => a + b, 0)} in 24 hours`}>
      <polyline points={pts} fill="none" strokeWidth="1.5" vectorEffect="non-scaling-stroke" className={tone === "warn" ? "stroke-amber-500" : "stroke-destructive"} />
    </svg>
  );
}

/** The app's events over a range, all issues together. */
function Series({ svc }: { svc: Service }) {
  const [range, setRange] = useState<Range>("7d");
  const [hover, setHover] = useState<number | null>(null);
  const { span, interval } = ranges[range];
  const series = useQuery({
    queryKey: ["service-error-series", svc.id, range],
    queryFn: () => {
      const to = Math.ceil(Date.now() / 1000 / interval) * interval;
      return api.serviceErrorSeries(svc.id, { from: to - span, to, interval });
    },
    placeholderData: keepPreviousData,
    refetchInterval: 60_000,
  });
  const points = series.data ?? [];
  const top = Math.max(1, ...points.map((p) => p.n));
  const total = points.reduce((a, p) => a + p.n, 0);
  const fmt = (t: number) =>
    new Date(t * 1000).toLocaleString(undefined, interval < DAY ? { weekday: "short", hour: "2-digit", minute: "2-digit" } : { month: "short", day: "numeric" });
  const h = hover === null ? null : points[hover];
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-3">
        <span className="text-sm">
          <span className="font-medium tabular-nums">{total.toLocaleString()}</span> <span className="text-muted-foreground">events</span>
        </span>
        {h && (
          <span className="text-xs tabular-nums text-muted-foreground">
            {fmt(h.t)} · {h.n} events
          </span>
        )}
        <Segmented size="sm" className="ml-auto" value={range} onValueChange={(v) => setRange(v as Range)} aria-label="Range">
          {Object.entries(ranges).map(([k, v]) => (
            <SegmentedItem key={k} value={k}>
              {v.label}
            </SegmentedItem>
          ))}
        </Segmented>
      </div>
      {series.error ? (
        <ErrorText error={series.error} />
      ) : (
        <div className="flex h-24 items-end gap-px" role="img" aria-label="Error events over time" onPointerLeave={() => setHover(null)}>
          {points.map((p, i) => (
            <div key={p.t} className={cn("relative flex h-full flex-1 items-end rounded-t-sm", hover === i && "bg-muted")} onPointerEnter={() => setHover(i)}>
              <div className="w-full rounded-t-sm bg-destructive/70" style={{ height: `${(p.n / top) * 100}%` }} />
            </div>
          ))}
        </div>
      )}
      {points.length > 0 && (
        <div className="flex justify-between text-xs text-muted-foreground tabular-nums">
          <span>{fmt(points[0].t)}</span>
          <span>{fmt(points[points.length - 1].t)}</span>
        </div>
      )}
    </div>
  );
}


/** Above the deployments: the current deploy raises errors well above the release before. */
export function DeployErrorsBanner({ svc, canRollback, onIssues }: { svc: Service; canRollback: boolean; onIssues: () => void }) {
  const qc = useQueryClient();
  const de = useQuery({ queryKey: ["service-deploy-errors", svc.id], queryFn: () => api.serviceDeployErrors(svc.id), refetchInterval: 60_000 });
  const rollback = useMutation({
    meta: { error: "Couldn't roll back" },
    mutationFn: (deploymentId: string) => api.rollback(svc.id, deploymentId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["deployments", svc.id] }),
  });
  const d = de.data;
  if (!d?.spike) return null;
  const times = d.previousRate > 0 ? `${Math.round(d.rate / d.previousRate)}× the rate of ${d.previousRelease}` : `${d.previousRelease ? `none with ${d.previousRelease}` : "none before"}`;
  return (
    <div role="alert" className="mb-4 flex flex-wrap items-center justify-between gap-3 rounded-lg border border-amber-500/50 bg-amber-500/10 px-4 py-3 text-sm">
      <p>
        <span className="font-medium">
          <Mono>{d.release}</Mono> raised {d.events.toLocaleString()} errors in its first {Math.round(d.minutes)} minutes
        </span>{" "}
        ({times}){d.newIssues > 0 && `, ${d.newIssues} new ${d.newIssues === 1 ? "issue" : "issues"}`}.
      </p>
      <span className="flex gap-2">
        <Button size="sm" variant="outline" onClick={onIssues}>
          See issues
        </Button>
        {canRollback && d.previous && (
          <ConfirmDialog
            trigger={
              <Button size="sm" variant="destructive" loading={rollback.isPending}>
                Roll back to {d.previousRelease}
              </Button>
            }
            title="Roll back?"
            description={`Redeploys ${d.previousRelease}. Current settings are kept; the next regular deploy uses the image in Settings again.`}
            confirmLabel="Roll back"
            destructive={false}
            onConfirm={() => rollback.mutate(d.previous!)}
          />
        )}
      </span>
    </div>
  );
}
