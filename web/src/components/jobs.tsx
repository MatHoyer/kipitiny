import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CalendarClock, ChevronRight, Pause, Pencil, Play, Plus, Timer, Trash2 } from "lucide-react";
import { useState, type FormEvent } from "react";
import { CheckboxField, Empty, Meta, MetaItem, Section, StateBadge, statusIcon, triggeredBy } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { CronField, describeCron, jobPresets } from "@/components/cron-field";
import { DeploymentLogView } from "@/components/logs";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingTextarea } from "@/components/ui/floating-textarea";
import { formatDateTime, formatDuration, timeAgo } from "@/lib/format";
import { cn } from "@/lib/utils";
import { api, type Job, type JobRun, type Service } from "../api";

const took = (r: JobRun) => formatDuration(Math.max(0, (r.finishedAt ? new Date(r.finishedAt).getTime() : Date.now()) - new Date(r.createdAt).getTime()));

/** An app's scheduled jobs: commands run on a cron schedule in a one-off container of its image. */
export function JobsTab({ svc, canEdit, canRun }: { svc: Service; canEdit: boolean; canRun: boolean }) {
  const qc = useQueryClient();
  const key = ["jobs", svc.id];
  const jobs = useQuery({
    queryKey: key,
    queryFn: () => api.jobs(svc.id),
    refetchInterval: (q) => (q.state.data?.some((j) => j.lastRun?.status === "running") ? 2_000 : 15_000),
  });
  const [open, setOpen] = useState<string | null>(null);
  const deployed = !!svc.currentDeploymentId;

  return (
    <Section
      title="Jobs"
      description="Commands run on a schedule in a one-off container of this app's image, with its environment, networks and volumes."
      actions={canEdit && <JobDialog serviceId={svc.id} />}
    >
      {!deployed && <p className="mb-3 text-sm text-muted-foreground">Deploy the app before its jobs can run.</p>}
      {svc.stopped && <p className="mb-3 text-sm text-muted-foreground">The app is stopped: its jobs don't run.</p>}
      {jobs.data?.length === 0 && <Empty>No job yet.</Empty>}
      <ul className="-mx-3 -my-2 divide-y">
        {jobs.data?.map((j) => (
          <JobRow
            key={j.id}
            job={j}
            canEdit={canEdit}
            canRun={canRun && deployed && !svc.stopped}
            open={open === j.id}
            onOpenChange={(o) => setOpen(o ? j.id : null)}
            onChange={() => qc.invalidateQueries({ queryKey: key })}
          />
        ))}
      </ul>
    </Section>
  );
}

function JobRow({
  job: j,
  canEdit,
  canRun,
  open,
  onOpenChange,
  onChange,
}: {
  job: Job;
  canEdit: boolean;
  canRun: boolean;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onChange: () => void;
}) {
  const qc = useQueryClient();
  const running = j.lastRun?.status === "running";
  const run = useMutation({
    meta: { error: "Couldn't run the job" },
    mutationFn: () => api.runJob(j.id),
    onSuccess: () => {
      onChange();
      qc.invalidateQueries({ queryKey: ["job-runs", j.id] });
      onOpenChange(true);
    },
  });
  const toggle = useMutation({
    meta: { error: "Couldn't update the job" },
    mutationFn: () => api.updateJob(j.id, { name: j.name, command: j.command, cron: j.cron, timeoutSec: j.timeoutSec, enabled: !j.enabled }),
    onSuccess: onChange,
  });
  const remove = useMutation({ meta: { error: "Couldn't delete the job" }, mutationFn: () => api.deleteJob(j.id), onSuccess: onChange });

  return (
    <Collapsible asChild open={open} onOpenChange={onOpenChange}>
      <li className={cn(open && "bg-muted/30")}>
        <div className="flex items-center gap-2 pr-3">
          <CollapsibleTrigger className="group flex min-w-0 flex-1 items-center gap-3 py-3 pl-3 text-left outline-none focus-visible:bg-muted/50">
            <ChevronRight className="size-4 shrink-0 text-muted-foreground transition-transform group-data-[state=open]:rotate-90" />
            <div className={cn("min-w-0 flex-1", !j.enabled && "opacity-50")}>
              <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
                <span className="font-mono">{j.name}</span>
                {j.lastRun && <StateBadge state={j.lastRun.status} />}
                {!j.enabled && <span className="text-xs font-normal text-muted-foreground">paused</span>}
              </p>
              <p className="truncate font-mono text-xs text-muted-foreground" title={j.command}>
                {j.command}
              </p>
              <Meta className="mt-0.5">
                <MetaItem icon={CalendarClock}>{describeCron(j.cron, jobPresets)}</MetaItem>
                {j.enabled && j.nextRun && <MetaItem>next {formatDateTime(j.nextRun)}</MetaItem>}
                <MetaItem icon={Timer}>timeout {formatDuration(j.timeoutSec * 1000)}</MetaItem>
                {j.lastRun && <MetaItem>last {timeAgo(j.lastRun.createdAt)}</MetaItem>}
              </Meta>
            </div>
          </CollapsibleTrigger>
          <div className="flex gap-0.5">
            {canRun && (
              <Button variant="outline" size="xs" disabled={running || run.isPending} onClick={() => run.mutate()}>
                <Play data-icon="inline-start" />
                Run now
              </Button>
            )}
            {canEdit && (
              <>
                <JobDialog serviceId={j.serviceId} job={j} />
                <Button
                  variant="ghost"
                  size="icon-xs"
                  title={j.enabled ? "Pause" : "Resume"}
                  aria-label={j.enabled ? "Pause" : "Resume"}
                  onClick={() => toggle.mutate()}
                >
                  {j.enabled ? <Pause /> : <Play />}
                </Button>
                <ConfirmDialog
                  trigger={
                    <Button variant="ghost" size="icon-xs" title="Delete" aria-label="Delete" className="text-muted-foreground hover:text-destructive">
                      <Trash2 />
                    </Button>
                  }
                  title={`Delete the job ${j.name}?`}
                  description="Its run history and logs go with it; a run in progress is stopped."
                  onConfirm={() => remove.mutate()}
                />
              </>
            )}
          </div>
        </div>
        <CollapsibleContent>{open && <JobRuns job={j} />}</CollapsibleContent>
      </li>
    </Collapsible>
  );
}

function JobRuns({ job }: { job: Job }) {
  const runs = useQuery({
    queryKey: ["job-runs", job.id],
    queryFn: () => api.jobRuns(job.id),
    refetchInterval: (q) => (q.state.data?.some((r) => r.status === "running") ? 1_000 : 10_000),
  });
  const [open, setOpen] = useState<string | null>(null);
  if (runs.data?.length === 0) return <p className="px-3 pb-3 pl-10 text-sm text-muted-foreground">No run yet.</p>;
  // The newest run's log is open until another is picked.
  const shown = open ?? runs.data?.[0]?.id;
  return (
    <ul className="space-y-1 px-3 pb-3 pl-10">
      {runs.data?.map((r) => (
        <Collapsible asChild key={r.id} open={shown === r.id} onOpenChange={(o) => setOpen(o ? r.id : "")}>
          <li>
            <CollapsibleTrigger className="group flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs outline-none hover:bg-muted/50 focus-visible:bg-muted/50">
              <ChevronRight className="size-3.5 shrink-0 text-muted-foreground transition-transform group-data-[state=open]:rotate-90" />
              <span className="[&>svg]:size-4">{statusIcon[r.status]}</span>
              <span className="font-medium" title={formatDateTime(r.createdAt)}>
                {timeAgo(r.createdAt)}
              </span>
              <span className="min-w-0 truncate text-muted-foreground">
                {r.status === "running" ? `running for ${took(r)}` : took(r)}
                {r.exitCode !== undefined && <> · exit {r.exitCode}</>}
                {r.triggeredBy ? <> · by {triggeredBy(r.triggeredBy)}</> : <> · scheduled</>}
                {r.error && <span className="text-destructive"> · {r.error}</span>}
              </span>
            </CollapsibleTrigger>
            <CollapsibleContent>
              <RunLog job={job} run={r} />
            </CollapsibleContent>
          </li>
        </Collapsible>
      ))}
    </ul>
  );
}

function RunLog({ job, run }: { job: Job; run: JobRun }) {
  const log = useQuery({
    // Keyed on the status too: the log is read once more when the run ends.
    queryKey: ["job-run-log", run.id, run.status],
    queryFn: () => api.jobRunLog(job.id, run.id),
    refetchInterval: run.status === "running" ? 1_000 : false,
  });
  return <DeploymentLogView text={log.data} pending={log.isPending} className="mt-1 max-h-96" />;
}

const emptyForm = { name: "", command: "", timeout: "60" };
const formOf = (j?: Job) => (j ? { name: j.name, command: j.command, timeout: String(Math.round(j.timeoutSec / 60)) } : emptyForm);

/** Creates a job, or edits `job` when given. */
function JobDialog({ serviceId, job }: { serviceId: string; job?: Job }) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [cron, setCron] = useState(job?.cron ?? "0 3 * * *");
  const [form, setForm] = useState(() => formOf(job));
  const [enabled, setEnabled] = useState(job?.enabled ?? true);
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });

  const save = useMutation({
    meta: { error: job ? "Couldn't update the job" : "Couldn't create the job" },
    mutationFn: () => {
      const input = { name: form.name.trim(), command: form.command, cron, timeoutSec: Math.round(Number(form.timeout) * 60) || 0, enabled };
      return job ? api.updateJob(job.id, input) : api.createJob(serviceId, input);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["jobs", serviceId] });
      onOpenChange(false);
    },
  });
  const onOpenChange = (next: boolean) => {
    setOpen(next);
    // Opening an edit starts from the job as it is now; closing a new one clears it.
    if (next ? job : !job) {
      setCron(job?.cron ?? "0 3 * * *");
      setForm(formOf(job));
      setEnabled(job?.enabled ?? true);
      save.reset();
    }
  };
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        {job ? (
          <Button variant="ghost" size="icon-xs" title="Edit" aria-label="Edit">
            <Pencil />
          </Button>
        ) : (
          <Button variant="outline" size="xs">
            <Plus data-icon="inline-start" />
            New job
          </Button>
        )}
      </DialogTrigger>
      <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-lg">
        <form onSubmit={onSubmit} className="contents">
          <DialogHeader>
            <DialogTitle>{job ? `Edit job ${job.name}` : "New job"}</DialogTitle>
            <DialogDescription>
              Runs with <code className="font-mono">sh -c</code> in a new container of the deployed image, one run at a time. A run
              that exits non-zero or times out fails and notifies.
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 sm:grid-cols-2">
            <FloatingInput
              label="Name"
              required
              value={form.name}
              onChange={set("name")}
              placeholder="nightly-report"
              inputClassName="font-mono"
              className="sm:col-span-2"
            />
            <FloatingTextarea
              label="Command"
              required
              value={form.command}
              onChange={set("command")}
              placeholder="php artisan schedule:run"
              className="font-mono sm:col-span-2"
              rows={2}
            />
            <CronField value={cron} onChange={setCron} presets={jobPresets} inputClassName="sm:col-span-2 sm:order-last" />
            <FloatingInput label="Timeout (minutes)" type="number" min={1} max={1440} required value={form.timeout} onChange={set("timeout")} />
          </div>
          <CheckboxField
            label="Scheduled"
            description="A paused job keeps its history and can still be run by hand."
            checked={enabled}
            onCheckedChange={setEnabled}
          />
          <DialogFooter>
            <Button type="submit" disabled={save.isPending}>
              {job ? "Save" : "Add job"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
