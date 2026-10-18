import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Eye, MousePointerClick, UserRound } from "lucide-react";
import { useState } from "react";
import { Empty, ErrorText, Loading, Mono, Section } from "@/components/common";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { api, type AnalyticsVisit, type Service } from "../api";

const regionNames = typeof Intl.DisplayNames === "function" ? new Intl.DisplayNames(undefined, { type: "region" }) : null;

export function countryLabel(code: string) {
  const flag = String.fromCodePoint(...[...code].map((c) => 0x1f1a5 + c.charCodeAt(0)));
  return `${flag} ${regionNames?.of(code) ?? code}`;
}

export function duration(seconds: number) {
  if (seconds < 60) return `${Math.round(seconds)}s`;
  const m = Math.floor(seconds / 60);
  return m < 60 ? `${m}m ${Math.round(seconds % 60)}s` : `${Math.floor(m / 60)}h ${m % 60}m`;
}

const when = (t: number) => new Date(t * 1000).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });

/** Where the visitor came from: the referrer, else the UTM source. */
const source = (v: AnalyticsVisit) => v.referrer || v.utmSource || "Direct";

const PAGE = 25;

/** The range's visits, newest first; one opens to its journey. */
export function Visits({ svc, span, filters, onFilter }: { svc: Service; span: number; filters: string[]; onFilter: (filter: string) => void }) {
  const [open, setOpen] = useState<string | null>(null);
  const visits = useInfiniteQuery({
    queryKey: ["service-visits", svc.id, span, filters],
    queryFn: ({ pageParam }) => {
      const to = Math.ceil(Date.now() / 1000 / 60) * 60;
      return api.serviceVisits(svc.id, { from: to - span, to, filters, before: pageParam, limit: PAGE });
    },
    initialPageParam: "",
    getNextPageParam: (last) => (last.length === PAGE ? last[last.length - 1].id : undefined),
    refetchInterval: 60_000,
  });
  const rows = visits.data?.pages.flat() ?? [];
  return (
    <Section
      title="Visits"
      description="Each visit's pages and events. Without cookies, a visitor is only recognized within a day, unless the app names them with kipitiny.identify."
    >
      {visits.isPending ? (
        <Loading />
      ) : visits.error ? (
        <ErrorText error={visits.error} />
      ) : rows.length === 0 ? (
        <Empty>No visits in this range.</Empty>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="text-xs text-muted-foreground">
              <tr className="text-left">
                <th className="pb-1.5 font-normal">Started</th>
                <th className="pb-1.5 font-normal">Visitor</th>
                <th className="pb-1.5 font-normal">Source</th>
                <th className="pb-1.5 text-right font-normal">Pages</th>
                <th className="pb-1.5 text-right font-normal">Events</th>
                <th className="pb-1.5 text-right font-normal">Duration</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((v) => (
                <tr key={v.id} className="cursor-pointer border-t hover:bg-muted/40" onClick={() => setOpen(v.id)}>
                  <td className="py-1.5 pr-3 whitespace-nowrap">
                    <button type="button" className="hover:underline focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none">
                      {when(v.startedAt)}
                    </button>
                  </td>
                  <td className="max-w-56 truncate py-1.5 pr-3">
                    {v.identity ? (
                      <span className="inline-flex items-center gap-1">
                        <UserRound className="size-3.5" />
                        {v.identity}
                      </span>
                    ) : (
                      <span className="text-muted-foreground">
                        {[v.country && countryLabel(v.country), v.browser, v.os, v.device].filter(Boolean).join(" · ")}
                      </span>
                    )}
                  </td>
                  <td className="max-w-48 truncate py-1.5 pr-3">{source(v)}</td>
                  <td className="py-1.5 text-right tabular-nums">{v.pageviews}</td>
                  <td className="py-1.5 text-right tabular-nums">{v.events}</td>
                  <td className="py-1.5 text-right text-muted-foreground tabular-nums">{duration(v.lastAt - v.startedAt)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {visits.hasNextPage && (
            <Button variant="outline" size="sm" className="mt-3" loading={visits.isFetchingNextPage} onClick={() => visits.fetchNextPage()}>
              Load more
            </Button>
          )}
        </div>
      )}
      <VisitSheet
        svc={svc}
        id={open}
        onClose={() => setOpen(null)}
        onFilter={(f) => {
          setOpen(null);
          onFilter(f);
        }}
      />
    </Section>
  );
}

/** A visit's journey: pages and events in order, with their times. */
function VisitSheet({ svc, id, onClose, onFilter }: { svc: Service; id: string | null; onClose: () => void; onFilter: (filter: string) => void }) {
  const visit = useQuery({ queryKey: ["service-visit", svc.id, id], queryFn: () => api.serviceVisit(svc.id, id!), enabled: !!id });
  const v = visit.data;
  return (
    <Sheet open={!!id} onOpenChange={(o) => !o && onClose()}>
      <SheetContent className="gap-0 overflow-y-auto data-[side=right]:sm:max-w-lg">
        <SheetHeader>
          <SheetTitle>{v ? `Visit of ${when(v.startedAt)}` : "Visit"}</SheetTitle>
          <SheetDescription>
            {v
              ? `${v.pageviews} ${v.pageviews === 1 ? "page" : "pages"}, ${v.events} ${v.events === 1 ? "event" : "events"}, ${duration(v.lastAt - v.startedAt)}`
              : "Loading…"}
          </SheetDescription>
        </SheetHeader>
        <div className="space-y-5 px-4 pb-6">
          {visit.isPending ? (
            <Loading />
          ) : visit.error ? (
            <ErrorText error={visit.error} />
          ) : (
            v && (
              <>
                <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
                  {v.identity && (
                    <>
                      <dt className="text-muted-foreground">Identity</dt>
                      <dd className="min-w-0">
                        <span className="break-all">{v.identity}</span>
                        {v.identityProps && <Props props={v.identityProps} />}
                        <Button variant="link" size="sm" className="h-auto p-0" onClick={() => onFilter(`identities:${v.identity}`)}>
                          All of this user's visits
                        </Button>
                      </dd>
                    </>
                  )}
                  <Row label="Source" value={source(v)} />
                  <Row label="Campaign" value={[v.utmSource, v.utmMedium, v.utmCampaign].filter(Boolean).join(" / ")} />
                  <Row label="Country" value={v.country && countryLabel(v.country)} />
                  <Row label="Device" value={[v.browser, v.os, v.device].filter(Boolean).join(" · ")} />
                  <Row label="Screen" value={v.screen} />
                  <Row label="Language" value={v.language} />
                </dl>
                <ol className="relative space-y-3 border-l pl-4">
                  {v.journey.map((e, i) => (
                    <li key={i} className="relative">
                      <span className="absolute top-0.5 -left-[25px] flex size-4.5 items-center justify-center rounded-full border bg-background [&>svg]:size-2.5">
                        {e.name ? <MousePointerClick /> : <Eye />}
                      </span>
                      <div className="flex items-baseline justify-between gap-2">
                        <span className="min-w-0 truncate font-medium">{e.name || e.path}</span>
                        <span className="shrink-0 text-xs text-muted-foreground tabular-nums">+{duration(e.at - v.startedAt)}</span>
                      </div>
                      <p className="truncate text-xs text-muted-foreground">
                        {e.name ? <>on {e.path}</> : e.title}
                        {e.query && <> · ?{e.query}</>}
                      </p>
                      {e.props && <Props props={e.props} />}
                    </li>
                  ))}
                </ol>
              </>
            )
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  if (!value) return null;
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 truncate">{value}</dd>
    </>
  );
}

function Props({ props }: { props: Record<string, unknown> }) {
  return (
    <div className="mt-1 flex flex-wrap gap-1">
      {Object.entries(props).map(([k, val]) => (
        <Mono key={k}>
          {k}={String(val)}
        </Mono>
      ))}
    </div>
  );
}
