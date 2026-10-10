import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { HardDrive, Pause, Pencil, Play, Plus, Trash2 } from "lucide-react";
import { useState, type FormEvent } from "react";
import { CheckboxField, Empty } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { CronField, describeCron } from "@/components/cron-field";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingSelect } from "@/components/ui/floating-select";
import { formatDateTime } from "@/lib/format";
import { cn } from "@/lib/utils";
import { storageIcon } from "@/components/storage";
import { api, type BackupTarget, type PgDatabase, type Schedule, type ScheduleInput } from "../api";

function describeRetention(s: ScheduleInput): string {
  const parts = [
    s.keepLast && `last ${s.keepLast}`,
    s.keepDaily && `${s.keepDaily} daily`,
    s.keepWeekly && `${s.keepWeekly} weekly`,
    s.keepMonthly && `${s.keepMonthly} monthly`,
  ].filter(Boolean);
  return parts.length ? `keep ${parts.join(", ")}` : "keep everything";
}

/** A service's backup schedules, or the manager's own without serviceId. */
export function Schedules({ serviceId, targets, databases = [] }: { serviceId?: string; targets: BackupTarget[]; databases?: PgDatabase[] }) {
  const qc = useQueryClient();
  const key = ["schedules", serviceId ?? "manager"];
  const schedules = useQuery({ queryKey: key, queryFn: () => (serviceId ? api.schedules(serviceId) : api.managerSchedules()) });
  const invalidate = () => qc.invalidateQueries({ queryKey: key });
  const toggle = useMutation({
    meta: { error: "Couldn't update the schedule" },
    mutationFn: ({ id, targetId, database, cron, keepLast, keepDaily, keepWeekly, keepMonthly, enabled, verify }: Schedule) =>
      api.updateSchedule(id, { targetId, database, cron, keepLast, keepDaily, keepWeekly, keepMonthly, verify, enabled: !enabled }),
    onSuccess: invalidate,
  });
  const remove = useMutation({ meta: { error: "Couldn't delete the schedule" }, mutationFn: api.deleteSchedule, onSuccess: invalidate });
  const targetOf = (id: string) => targets.find((t) => t.id === id);

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium">Schedules</h3>
        <ScheduleDialog serviceId={serviceId} targets={targets} databases={databases} />
      </div>
      {schedules.data?.length === 0 && (
        <Empty>No schedule: {serviceId ? "this service" : "the manager"} is only backed up manually.</Empty>
      )}
      <ul className="space-y-2">
        {schedules.data?.map((s) => {
          const target = targetOf(s.targetId);
          return (
            <li key={s.id} className="flex items-center justify-between gap-3 rounded-lg border px-3 py-2 text-sm">
              <div className={cn("flex min-w-0 items-center gap-3", !s.enabled && "opacity-50")}>
                <span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-muted [&>svg]:size-4">
                  {target ? storageIcon(target) : <HardDrive />}
                </span>
                <div className="min-w-0">
                  <span className="font-medium">{describeCron(s.cron)}</span>
                  <span className="text-muted-foreground"> → {target?.name ?? s.targetId}</span>
                  {s.database && <span className="ml-2 font-mono text-xs text-muted-foreground">{s.database}</span>}
                  <p className="text-xs text-muted-foreground">
                    {describeRetention(s)}
                    {s.verify && " · restore-tested"}
                    {s.enabled && s.nextRun && ` · next ${formatDateTime(s.nextRun)}`}
                  </p>
                </div>
              </div>
              <div className="flex gap-0.5">
                <ScheduleDialog serviceId={serviceId} targets={targets} databases={databases} schedule={s} />
                <Button
                  variant="ghost"
                  size="icon-xs"
                  title={s.enabled ? "Pause" : "Resume"}
                  aria-label={s.enabled ? "Pause" : "Resume"}
                  onClick={() => toggle.mutate(s)}
                >
                  {s.enabled ? <Pause /> : <Play />}
                </Button>
                <ConfirmDialog
                  trigger={
                    <Button variant="ghost" size="icon-xs" title="Delete" aria-label="Delete" className="text-muted-foreground hover:text-destructive">
                      <Trash2 />
                    </Button>
                  }
                  title="Delete this schedule?"
                  description="Its backups are kept."
                  onConfirm={() => remove.mutate(s.id)}
                />
              </div>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

const defaultCron = "0 3 * * *";
const emptyForm = { targetId: "local", keepLast: "0", keepDaily: "7", keepWeekly: "4", keepMonthly: "6" };

const formOf = (s?: Schedule) =>
  s
    ? { targetId: s.targetId, keepLast: String(s.keepLast), keepDaily: String(s.keepDaily), keepWeekly: String(s.keepWeekly), keepMonthly: String(s.keepMonthly) }
    : emptyForm;

/** Creates a schedule, or edits `schedule` when given. */
function ScheduleDialog({
  serviceId,
  targets,
  databases,
  schedule,
}: {
  serviceId?: string;
  targets: BackupTarget[];
  databases: PgDatabase[];
  schedule?: Schedule;
}) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [cron, setCron] = useState(schedule?.cron ?? defaultCron);
  const [form, setForm] = useState(() => formOf(schedule));
  const [verify, setVerify] = useState(schedule?.verify ?? true);
  // Empty follows the service's own database.
  const [database, setDatabase] = useState(schedule?.database ?? "");
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });

  const save = useMutation({
    meta: { error: schedule ? "Couldn't update the schedule" : "Couldn't create the schedule" },
    mutationFn: () => {
      const input = {
        targetId: form.targetId,
        database,
        cron,
        keepLast: Number(form.keepLast) || 0,
        keepDaily: Number(form.keepDaily) || 0,
        keepWeekly: Number(form.keepWeekly) || 0,
        keepMonthly: Number(form.keepMonthly) || 0,
        enabled: schedule?.enabled ?? true,
        verify: !!serviceId && verify,
      };
      if (schedule) return api.updateSchedule(schedule.id, input);
      return serviceId ? api.createSchedule(serviceId, input) : api.createManagerSchedule(input);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["schedules", serviceId ?? "manager"] });
      onOpenChange(false);
    },
  });
  const onOpenChange = (next: boolean) => {
    setOpen(next);
    // Opening an edit starts from the schedule as it is now; closing a new one clears it.
    if (next ? schedule : !schedule) {
      setCron(schedule?.cron ?? defaultCron);
      setForm(formOf(schedule));
      setVerify(schedule?.verify ?? true);
      setDatabase(schedule?.database ?? "");
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
        {schedule ? (
          <Button variant="ghost" size="icon-xs" title="Edit" aria-label="Edit">
            <Pencil />
          </Button>
        ) : (
          <Button variant="outline" size="xs">
            <Plus data-icon="inline-start" />
            Add schedule
          </Button>
        )}
      </DialogTrigger>
      <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-lg">
        <form onSubmit={onSubmit} className="contents">
          <DialogHeader>
            <DialogTitle>{schedule ? "Edit backup schedule" : "New backup schedule"}</DialogTitle>
            <DialogDescription>
              After each run, older backups from this schedule are deleted unless a rule keeps them (newest of each day,
              week, month). All zeros keeps everything. Manual backups are never pruned.
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 sm:grid-cols-2">
            <CronField value={cron} onChange={setCron} inputClassName="sm:col-span-2 sm:order-last" />
            <FloatingSelect
              label="Storage"
              value={form.targetId}
              onValueChange={(v) => setForm({ ...form, targetId: v })}
              options={targets.map((t) => ({
                value: t.id,
                label: (
                  <span className="flex items-center gap-2 [&>svg]:size-4">
                    {storageIcon(t)}
                    {t.name}
                  </span>
                ),
              }))}
            />
            {databases.length > 1 && (
              <FloatingSelect
                label="Database"
                value={database || "-"}
                onValueChange={(v) => setDatabase(v === "-" ? "" : v)}
                className="sm:col-span-2"
                options={[
                  { value: "-", label: `The service's own (${databases.find((d) => d.main)?.name ?? "main"})` },
                  ...databases.filter((d) => !d.main).map((d) => ({ value: d.name, label: <span className="font-mono">{d.name}</span> })),
                ]}
              />
            )}
          </div>
          <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <FloatingInput label="Keep last" type="number" min={0} value={form.keepLast} onChange={set("keepLast")} />
            <FloatingInput label="Daily" type="number" min={0} value={form.keepDaily} onChange={set("keepDaily")} />
            <FloatingInput label="Weekly" type="number" min={0} value={form.keepWeekly} onChange={set("keepWeekly")} />
            <FloatingInput label="Monthly" type="number" min={0} value={form.keepMonthly} onChange={set("keepMonthly")} />
          </div>
          {serviceId && (
            <CheckboxField
              label="Restore-test every backup"
              description="Restores it into a throwaway, network-less container and checks the result (a database dump is loaded into PostgreSQL, a volume archive read back entirely). A backup you have never restored is a hope, not a backup."
              checked={verify}
              onCheckedChange={setVerify}
            />
          )}
          <DialogFooter>
            <Button type="submit" disabled={save.isPending}>
              {schedule ? "Save" : "Add schedule"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
