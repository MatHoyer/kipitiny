import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Bug, ChevronLeft, ChevronRight, Clock, Rocket, Users } from "lucide-react";
import { useState } from "react";
import { ErrorText, Mono, Tag } from "@/components/common";
import { Loading } from "@kipit-labs/kipitidy/loading";
import { Meta, MetaItem } from "@kipit-labs/kipitidy/meta";
import { Button } from "@kipit-labs/kipitidy/button";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@kipit-labs/kipitidy/sheet";
import { timeAgo } from "@/lib/format";
import { cn } from "@/lib/utils";
import { countryLabel } from "../analytics/analytics-visits";
import { api, type ErrorEvent, type ErrorException, type Issue, type IssueStatus, type Service } from "../../api";

const DAY = 86400;
const ago = (t: number) => timeAgo(new Date(t * 1000).toISOString());

export function IssueBadge({ issue }: { issue: Issue }) {
  if (issue.status === "open" && issue.regressedAt) return <Tag className="bg-amber-500/15 text-amber-700 dark:text-amber-400">Regressed</Tag>;
  if (issue.status === "open" && Date.now() / 1000 - issue.firstSeen < DAY) return <Tag className="bg-destructive/10 text-destructive">New</Tag>;
  if (issue.status !== "open") return <Tag>{issue.status === "resolved" ? "Resolved" : "Ignored"}</Tag>;
  return null;
}

/** An issue and its kept events, one at a time. */
export function IssueSheet({ svc, id, canChange, onClose }: { svc: Service; id: number | null; canChange: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const [event, setEvent] = useState("");
  const issue = useQuery({ queryKey: ["service-issue", svc.id, id], queryFn: () => api.serviceIssue(svc.id, id!), enabled: !!id });
  const ev = useQuery({
    queryKey: ["service-issue-event", svc.id, id, event],
    queryFn: () => api.serviceIssueEvent(svc.id, id!, event),
    enabled: !!id,
    placeholderData: keepPreviousData,
  });
  const setStatus = useMutation({
    meta: { error: "Couldn't change the issue" },
    mutationFn: (status: IssueStatus) => api.setIssueStatus(svc.id, id!, status),
    onSuccess: (updated) => {
      qc.setQueryData(["service-issue", svc.id, id], updated);
      qc.invalidateQueries({ queryKey: ["service-errors", svc.id] });
    },
  });
  const is = issue.data;
  return (
    <Sheet
      open={!!id}
      onOpenChange={(o) => {
        if (!o) {
          setEvent("");
          onClose();
        }
      }}
    >
      <SheetContent className="gap-0 overflow-y-auto data-[side=right]:sm:max-w-2xl">
        <SheetHeader>
          <SheetTitle className="pr-6 break-words">{is?.title ?? "Issue"}</SheetTitle>
          <SheetDescription asChild>
            <div className="space-y-2">
              {is && (
                <Meta>
                  <IssueBadge issue={is} />
                  <MetaItem icon={Bug}>{is.events.toLocaleString()} events</MetaItem>
                  <MetaItem icon={Users}>{is.users.toLocaleString()} users</MetaItem>
                  <MetaItem icon={Clock}>
                    first {ago(is.firstSeen)}, last {ago(is.lastSeen)}
                  </MetaItem>
                  {is.firstRelease && (
                    <MetaItem icon={Rocket}>
                      since <Mono>{is.firstRelease}</Mono>
                    </MetaItem>
                  )}
                </Meta>
              )}
              {is && canChange && (
                <div className="flex flex-wrap gap-2 pt-1">
                  {is.status !== "resolved" && (
                    <Button size="sm" loading={setStatus.isPending && setStatus.variables === "resolved"} onClick={() => setStatus.mutate("resolved")}>
                      Resolve
                    </Button>
                  )}
                  {is.status !== "ignored" && (
                    <Button size="sm" variant="outline" onClick={() => setStatus.mutate("ignored")} disabled={setStatus.isPending}>
                      Ignore
                    </Button>
                  )}
                  {is.status !== "open" && (
                    <Button size="sm" variant="outline" onClick={() => setStatus.mutate("open")} disabled={setStatus.isPending}>
                      Reopen
                    </Button>
                  )}
                </div>
              )}
            </div>
          </SheetDescription>
        </SheetHeader>
        <div className="space-y-6 px-4 pb-6">
          {(issue.error || ev.error) && <ErrorText error={issue.error ?? ev.error} />}
          {ev.isPending && !ev.error ? (
            <Loading />
          ) : (
            ev.data && (
              <>
                <div className="flex items-center justify-between gap-2 text-sm">
                  <span className="text-muted-foreground">
                    Event {ev.data.index} of {ev.data.kept} kept · {new Date(ev.data.event.at * 1000).toLocaleString()}
                  </span>
                  <span className="flex gap-1">
                    <Button size="icon-sm" variant="outline" aria-label="Newer event" disabled={!ev.data.newer} onClick={() => setEvent(ev.data!.newer!)}>
                      <ChevronLeft />
                    </Button>
                    <Button size="icon-sm" variant="outline" aria-label="Older event" disabled={!ev.data.older} onClick={() => setEvent(ev.data!.older!)}>
                      <ChevronRight />
                    </Button>
                  </span>
                </div>
                <EventDetail event={ev.data.event} />
              </>
            )
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}

const contextLabels: Record<string, string> = { browser: "Browser", os: "OS", runtime: "Runtime", device: "Device" };

function EventDetail({ event: e }: { event: ErrorEvent }) {
  const facts: [string, string | undefined][] = [
    ["Release", e.release],
    ["Environment", e.environment],
    ["User", e.user],
    ["Request", e.url && [e.method, e.url].filter(Boolean).join(" ")],
    ["Transaction", e.transaction],
    ["Server", e.serverName],
    ["Country", e.country && countryLabel(e.country)],
    ...Object.entries(e.contexts ?? {}).map(([k, v]): [string, string] => [contextLabels[k] ?? k, v]),
    ["SDK", e.sdk],
  ];
  const exceptions = [...(e.exceptions ?? [])].reverse(); // the raised one first
  return (
    <>
      {e.message && <p className="rounded-md bg-muted p-3 font-mono text-sm break-words whitespace-pre-wrap">{e.message}</p>}
      {exceptions.map((x, i) => (
        <ExceptionView key={i} x={x} cause={i > 0} />
      ))}
      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
        {facts
          .filter(([, v]) => v)
          .map(([k, v]) => (
            <div key={k} className="contents">
              <dt className="text-muted-foreground">{k}</dt>
              <dd className="min-w-0 break-words">{v}</dd>
            </div>
          ))}
      </dl>
      {e.tags && Object.keys(e.tags).length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {Object.entries(e.tags).map(([k, v]) => (
            <Tag key={k} className="font-mono">
              {k}: {v}
            </Tag>
          ))}
        </div>
      )}
      {e.breadcrumbs && e.breadcrumbs.length > 0 && (
        <div className="space-y-1.5">
          <h3 className="text-sm font-medium">Breadcrumbs</h3>
          <ol className="divide-y rounded-md border text-sm">
            {e.breadcrumbs.map((b, i) => (
              <li key={i} className={cn("grid grid-cols-[5rem_6rem_1fr] gap-2 px-3 py-1.5", b.level === "error" && "text-destructive")}>
                <span className="font-mono text-xs text-muted-foreground tabular-nums">
                  {b.at ? new Date(b.at * 1000).toLocaleTimeString() : ""}
                </span>
                <span className="truncate font-mono text-xs text-muted-foreground">{b.category || b.type}</span>
                <span className="min-w-0 break-words">
                  {b.message || [b.data?.method, b.data?.url ?? b.data?.to, b.data?.status_code].filter(Boolean).join(" ")}
                </span>
              </li>
            ))}
          </ol>
        </div>
      )}
    </>
  );
}

/** An exception and its stack, innermost frame first; library frames folded. */
function ExceptionView({ x, cause }: { x: ErrorException; cause: boolean }) {
  const [all, setAll] = useState(false);
  const frames = [...(x.frames ?? [])].reverse();
  const own = frames.some((f) => f.inApp);
  const shown = all || !own ? frames : frames.filter((f) => f.inApp);
  return (
    <div className="space-y-1.5">
      <h3 className="text-sm font-medium break-words">
        {cause && <span className="text-muted-foreground">Caused by </span>}
        {x.type}
        {x.value && <span className="font-normal text-muted-foreground">: {x.value}</span>}
        {x.handled === false && <Tag className="ml-2 bg-destructive/10 text-destructive">Unhandled</Tag>}
      </h3>
      {shown.length > 0 && (
        <div className="overflow-hidden rounded-md border font-mono text-xs">
          {shown.map((f, i) => (
            <div key={i} className={cn("border-t first:border-t-0", !f.inApp && "bg-muted/50 text-muted-foreground")}>
              <div className="flex flex-wrap justify-between gap-x-3 px-3 py-1.5">
                <span className="break-all">{f.function || "?"}</span>
                <span className="break-all">
                  {f.file || f.module}
                  {f.line ? `:${f.line}` : ""}
                </span>
              </div>
              {f.context && i === shown.findIndex((g) => g.context) && (
                <pre className="overflow-x-auto border-t bg-muted/40 py-1.5">
                  {f.context.map((line, j) => {
                    const n = (f.line ?? 0) - (f.contextLine ?? 0) + j;
                    return (
                      <div key={j} className={cn("px-3", j === f.contextLine && "bg-destructive/10 text-destructive")}>
                        <span className="mr-3 inline-block w-8 text-right text-muted-foreground select-none">{n}</span>
                        {line}
                      </div>
                    );
                  })}
                </pre>
              )}
            </div>
          ))}
        </div>
      )}
      {own && frames.length > shown.length && (
        <button type="button" className="text-xs text-muted-foreground hover:underline" onClick={() => setAll(true)}>
          Show {frames.length - shown.length} library frames
        </button>
      )}
    </div>
  );
}
