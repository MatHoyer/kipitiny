import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pause, Play, Plus, Trash2 } from "lucide-react";
import { useState, type FormEvent } from "react";
import { CheckboxField, Empty, Mono } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
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
import { cn } from "@/lib/utils";
import { api, type BackupTarget, type Schedule, type ScheduleInput } from "../api";

const presets: [string, string][] = [
  ["0 * * * *", "Every hour"],
  ["0 3 * * *", "Daily at 03:00 UTC"],
  ["0 3 * * 0", "Weekly, Sunday 03:00 UTC"],
];

function describeCron(cron: string): string {
  return presets.find(([c]) => c === cron)?.[1] ?? cron;
}

function describeRetention(s: ScheduleInput): string {
  const parts = [
    s.keepLast && `last ${s.keepLast}`,
    s.keepDaily && `${s.keepDaily} daily`,
    s.keepWeekly && `${s.keepWeekly} weekly`,
    s.keepMonthly && `${s.keepMonthly} monthly`,
  ].filter(Boolean);
  return parts.length ? `keep ${parts.join(", ")}` : "keep everything";
}

export function Schedules({ serviceId, targets }: { serviceId: string; targets: BackupTarget[] }) {
  const qc = useQueryClient();
  const schedules = useQuery({ queryKey: ["schedules", serviceId], queryFn: () => api.schedules(serviceId) });
  const invalidate = () => qc.invalidateQueries({ queryKey: ["schedules", serviceId] });
  const toggle = useMutation({
    meta: { error: "Couldn't update the schedule" },
    mutationFn: ({ id, targetId, cron, keepLast, keepDaily, keepWeekly, keepMonthly, enabled, verify }: Schedule) =>
      api.updateSchedule(id, { targetId, cron, keepLast, keepDaily, keepWeekly, keepMonthly, verify, enabled: !enabled }),
    onSuccess: invalidate,
  });
  const remove = useMutation({ meta: { error: "Couldn't delete the schedule" }, mutationFn: api.deleteSchedule, onSuccess: invalidate });
  const targetName = (id: string) => targets.find((t) => t.id === id)?.name ?? id;

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium">Schedules</h3>
        <ScheduleDialog serviceId={serviceId} targets={targets} />
      </div>
      {schedules.data?.length === 0 && <Empty>No schedule: this database is only backed up manually.</Empty>}
      <ul className="space-y-2">
        {schedules.data?.map((s) => (
          <li key={s.id} className="flex flex-wrap items-center justify-between gap-2 rounded-lg border px-3 py-2 text-sm">
            <div className={cn(!s.enabled && "opacity-50")}>
              <span className="font-medium">{describeCron(s.cron)}</span>
              <span className="text-muted-foreground"> → {targetName(s.targetId)}</span>
              <p className="text-xs text-muted-foreground">
                {describeRetention(s)}
                {s.verify && " · restore-tested"}
                {s.enabled && s.nextRun && ` · next ${new Date(s.nextRun).toLocaleString()}`}
              </p>
            </div>
            <div className="flex gap-0.5">
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
        ))}
      </ul>
    </div>
  );
}

const emptyForm = { targetId: "local", keepLast: "0", keepDaily: "7", keepWeekly: "4", keepMonthly: "6" };

function ScheduleDialog({ serviceId, targets }: { serviceId: string; targets: BackupTarget[] }) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [preset, setPreset] = useState(presets[1][0]);
  const [custom, setCustom] = useState("");
  const [form, setForm] = useState(emptyForm);
  const [verify, setVerify] = useState(true);
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });

  const create = useMutation({
    meta: { error: "Couldn't create the schedule" },
    mutationFn: () =>
      api.createSchedule(serviceId, {
        targetId: form.targetId,
        cron: preset === "custom" ? custom : preset,
        keepLast: Number(form.keepLast) || 0,
        keepDaily: Number(form.keepDaily) || 0,
        keepWeekly: Number(form.keepWeekly) || 0,
        keepMonthly: Number(form.keepMonthly) || 0,
        enabled: true,
        verify,
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["schedules", serviceId] });
      onOpenChange(false);
    },
  });
  const onOpenChange = (next: boolean) => {
    setOpen(next);
    if (!next) {
      setPreset(presets[1][0]);
      setCustom("");
      setForm(emptyForm);
      setVerify(true);
      create.reset();
    }
  };
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    create.mutate();
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button variant="outline" size="xs">
          <Plus data-icon="inline-start" />
          Add schedule
        </Button>
      </DialogTrigger>
      <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-lg">
        <form onSubmit={onSubmit} className="contents">
          <DialogHeader>
            <DialogTitle>New backup schedule</DialogTitle>
            <DialogDescription>
              After each run, older backups from this schedule are deleted unless a rule keeps them (newest of each day,
              week, month). All zeros keeps everything. Manual backups are never pruned.
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 sm:grid-cols-2">
            <FloatingSelect
              label="When"
              value={preset}
              onValueChange={setPreset}
              options={[...presets.map(([value, label]) => ({ value, label })), { value: "custom", label: "Custom cron…" }]}
            />
            <FloatingSelect
              label="Target"
              value={form.targetId}
              onValueChange={(v) => setForm({ ...form, targetId: v })}
              options={targets.map((t) => ({ value: t.id, label: t.name }))}
            />
            {preset === "custom" && (
              <FloatingInput
                label="Cron expression"
                required
                value={custom}
                onChange={(e) => setCustom(e.target.value)}
                placeholder="30 2 * * 1-5"
                inputClassName="font-mono"
                className="sm:col-span-2"
                description={
                  <>
                    5 fields, UTC. Prefix with <Mono>CRON_TZ=Europe/Paris</Mono> for another zone.
                  </>
                }
              />
            )}
          </div>
          <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <FloatingInput label="Keep last" type="number" min={0} value={form.keepLast} onChange={set("keepLast")} />
            <FloatingInput label="Daily" type="number" min={0} value={form.keepDaily} onChange={set("keepDaily")} />
            <FloatingInput label="Weekly" type="number" min={0} value={form.keepWeekly} onChange={set("keepWeekly")} />
            <FloatingInput label="Monthly" type="number" min={0} value={form.keepMonthly} onChange={set("keepMonthly")} />
          </div>
          <CheckboxField
            label="Restore-test every backup"
            description="Restores it into a throwaway, network-less PostgreSQL container and checks the result. A backup you have never restored is a hope, not a backup."
            checked={verify}
            onCheckedChange={setVerify}
          />
          <DialogFooter>
            <Button type="submit" disabled={create.isPending}>
              Add schedule
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
