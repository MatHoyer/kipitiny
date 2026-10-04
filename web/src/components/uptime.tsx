import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ExternalLink, HeartPulse, Pencil, Trash2 } from "lucide-react";
import { useState, type FormEvent } from "react";
import { CheckboxField, Empty, Section, StateBadge } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Sparkline } from "@/components/usage";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { Tip } from "@/components/ui/tooltip";
import { formatDateTime, timeAgo } from "@/lib/format";
import { cn } from "@/lib/utils";
import { api, type Service, type Uptime, type UptimeResult } from "../api";

const pct = (v: number | null) => (v === null ? "–" : `${v >= 99.995 ? 100 : v.toFixed(2)}%`);

/** The uptime check of an app with a public domain: its state, its record, and its settings. */
export function UptimeSection({ svc }: { svc: Service }) {
  const uptime = useQuery({ queryKey: ["uptime", svc.id], queryFn: () => api.uptime(svc.id), refetchInterval: 15_000 });
  const u = uptime.data;
  const chk = u?.check;

  if (!svc.domain && !chk) return null;
  return (
    <Section
      title="Uptime"
      description={
        u?.url ? (
          <a href={u.url} target="_blank" rel="noreferrer" className="inline-flex min-w-0 items-center gap-1 font-mono underline-offset-4 hover:underline">
            <span className="truncate">{u.url}</span>
            <ExternalLink className="size-3.5 shrink-0" />
          </a>
        ) : (
          "Request the public URL on an interval and notify when it goes down."
        )
      }
      actions={u && <UptimeDialog svc={svc} uptime={u} />}
    >
      {!u ? null : !chk ? (
        <Empty>No uptime check.</Empty>
      ) : (
        <div className="space-y-4">
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <StateBadge state={u.problem ? "paused" : chk.down ? "down" : u.recent.length ? "up" : "waiting"} />
            {u.problem && u.problem !== "paused" && <span className="text-muted-foreground">Not checking: {u.problem}.</span>}
            {!u.problem && chk.changedAt && (
              <span className="text-muted-foreground" title={formatDateTime(chk.changedAt)}>
                {chk.down ? "down" : "up"} since {timeAgo(chk.changedAt)}
              </span>
            )}
          </div>
          <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
            <Figure label="Last 24 hours" value={pct(u.uptime24h)} />
            <Figure label="Last 7 days" value={pct(u.uptime7d)} />
            <Figure label="Last 30 days" value={pct(u.uptime30d)} />
            <Figure label="Response time" value={u.avgLatencyMs === null ? "–" : `${u.avgLatencyMs} ms`} hint="average, 24 hours" />
          </div>
          <RecentChecks recent={u.recent} />
        </div>
      )}
    </Section>
  );
}

function Figure({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <Card size="sm" className="gap-0.5 px-3">
      <p className="text-xs text-muted-foreground">{label}</p>
      <p className="text-xl font-semibold tabular-nums">{value}</p>
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </Card>
  );
}

/** Results the manager keeps per check (uptimeRecent). */
const RECENT_SLOTS = 60;

function ResultTip({ r }: { r: UptimeResult }) {
  return (
    <div className="space-y-0.5">
      <p className="font-medium">{formatDateTime(r.at)}</p>
      <p>{r.ok ? `${r.status} in ${r.latencyMs} ms` : (r.error ?? "failed")}</p>
    </div>
  );
}

/** One bar per recent check, and their response times. */
function RecentChecks({ recent }: { recent: UptimeResult[] }) {
  if (recent.length === 0) return <p className="text-sm text-muted-foreground">No check since the manager started.</p>;
  const latencies = recent.filter((r) => r.ok).map((r) => r.latencyMs);
  // Empty slots first, so the bar keeps its width and fills from the right.
  const empty = Math.max(0, RECENT_SLOTS - recent.length);
  return (
    <div className="space-y-3">
      <div className="flex h-8 items-stretch gap-0.5" role="img" aria-label={`${recent.filter((r) => !r.ok).length} of the last ${recent.length} checks failed`}>
        {Array.from({ length: empty }, (_, i) => (
          <span key={`empty-${i}`} className="flex-1 rounded-[2px] bg-muted" />
        ))}
        {recent.map((r) => (
          <Tip key={r.at} content={<ResultTip r={r} />}>
            <span className={cn("flex-1 rounded-[2px] transition-opacity hover:opacity-70", r.ok ? "bg-emerald-500/80" : "bg-red-500")} />
          </Tip>
        ))}
      </div>
      {latencies.length >= 2 && (
        <div className="space-y-1">
          <p className="text-xs text-muted-foreground">Response time</p>
          <div className="text-primary">
            <Sparkline values={latencies} />
          </div>
        </div>
      )}
      <p className="text-xs text-muted-foreground">
        Last {recent.length} check{recent.length === 1 ? "" : "s"}; the latest {timeAgo(recent[recent.length - 1].at)}.
      </p>
    </div>
  );
}

const formOf = (u: Uptime) => ({
  path: u.check?.path ?? "/",
  interval: String(u.check?.intervalSec ?? 60),
  timeout: String(u.check?.timeoutSec ?? 10),
  status: u.check?.expectedStatus ? String(u.check.expectedStatus) : "",
  enabled: u.check?.enabled ?? true,
});

function UptimeDialog({ svc, uptime }: { svc: Service; uptime: Uptime }) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [form, setForm] = useState(() => formOf(uptime));
  const set = (k: "path" | "interval" | "timeout" | "status") => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });
  const onOpenChange = (o: boolean) => {
    if (o) setForm(formOf(uptime));
    setOpen(o);
  };
  const save = useMutation({
    meta: { error: "Couldn't save the uptime check" },
    mutationFn: () =>
      api.setUptime(svc.id, {
        path: form.path.trim(),
        intervalSec: Number(form.interval) || 0,
        timeoutSec: Number(form.timeout) || 0,
        expectedStatus: Number(form.status) || 0,
        enabled: form.enabled,
      }),
    onSuccess: (u) => {
      qc.setQueryData(["uptime", svc.id], u);
      setOpen(false);
    },
  });
  const remove = useMutation({
    meta: { error: "Couldn't delete the uptime check" },
    mutationFn: () => api.deleteUptime(svc.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["uptime", svc.id] });
      setOpen(false);
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button variant="outline" size="xs" disabled={!svc.domain && !uptime.check}>
          {uptime.check ? <Pencil data-icon="inline-start" /> : <HeartPulse data-icon="inline-start" />}
          {uptime.check ? "Edit" : "Add check"}
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={onSubmit} className="contents">
          <DialogHeader>
            <DialogTitle>Uptime check</DialogTitle>
            <DialogDescription>
              kipitiny requests https://{svc.domain || "…"}
              {form.path || "/"} on an interval. After 3 failures in a row, the channels subscribed to uptime events are
              notified; again once 2 checks in a row pass.
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 sm:grid-cols-2">
            <FloatingInput label="Path" required value={form.path} onChange={set("path")} className="font-mono sm:col-span-2" />
            <FloatingInput label="Interval (seconds)" type="number" min={30} max={3600} required value={form.interval} onChange={set("interval")} />
            <FloatingInput label="Timeout (seconds)" type="number" min={1} max={60} required value={form.timeout} onChange={set("timeout")} />
            <FloatingInput
              label="Expected status"
              type="number"
              min={100}
              max={599}
              value={form.status}
              onChange={set("status")}
              description="Empty accepts any status below 400. Redirects are not followed."
              className="sm:col-span-2"
            />
            <CheckboxField
              label="Enabled"
              checked={form.enabled}
              onCheckedChange={(enabled) => setForm({ ...form, enabled })}
              className="sm:col-span-2"
            />
          </div>
          <DialogFooter className="sm:justify-between">
            {uptime.check ? (
              <ConfirmDialog
                trigger={
                  <Button type="button" variant="ghost" size="sm" className="text-destructive" disabled={remove.isPending}>
                    <Trash2 data-icon="inline-start" />
                    Delete
                  </Button>
                }
                title="Delete the uptime check?"
                description="Its results are deleted with it."
                onConfirm={() => remove.mutate()}
              />
            ) : (
              <span />
            )}
            <Button type="submit" loading={save.isPending}>
              Save
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
