import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { api, type BackupTarget, type Schedule, type ScheduleInput } from "../api";
import { Button, ErrorText, Field, Input, Select } from "../ui";

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
  const [adding, setAdding] = useState(false);
  const invalidate = () => qc.invalidateQueries({ queryKey: ["schedules", serviceId] });
  const toggle = useMutation({
    mutationFn: ({ id, targetId, cron, keepLast, keepDaily, keepWeekly, keepMonthly, enabled, verify }: Schedule) =>
      api.updateSchedule(id, { targetId, cron, keepLast, keepDaily, keepWeekly, keepMonthly, verify, enabled: !enabled }),
    onSuccess: invalidate,
  });
  const remove = useMutation({ mutationFn: api.deleteSchedule, onSuccess: invalidate });
  const targetName = (id: string) => targets.find((t) => t.id === id)?.name ?? id;

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <h3 className="text-xs font-semibold tracking-wide text-zinc-500 uppercase">Schedules</h3>
        {!adding && (
          <button className="text-xs hover:underline" onClick={() => setAdding(true)}>
            Add schedule
          </button>
        )}
      </div>
      {schedules.data?.length === 0 && !adding && (
        <p className="text-sm text-zinc-500">No schedule: this database is only backed up manually.</p>
      )}
      <ul className="divide-y divide-zinc-200 text-sm dark:divide-zinc-800">
        {schedules.data?.map((s) => (
          <li key={s.id} className="flex flex-wrap items-center justify-between gap-2 py-1.5">
            <div className={s.enabled ? "" : "opacity-50"}>
              <span className="font-medium">{describeCron(s.cron)}</span>
              <span className="text-zinc-500"> → {targetName(s.targetId)}</span>
              <p className="text-xs text-zinc-500">
                {describeRetention(s)}
                {s.verify && " · restore-tested"}
                {s.enabled && s.nextRun && ` · next ${new Date(s.nextRun).toLocaleString()}`}
              </p>
            </div>
            <div className="space-x-3 text-xs">
              <button className="hover:underline" onClick={() => toggle.mutate(s)}>
                {s.enabled ? "Pause" : "Resume"}
              </button>
              <button
                className="text-red-600 hover:underline"
                onClick={() => confirm("Delete this schedule? Its backups are kept.") && remove.mutate(s.id)}
              >
                Delete
              </button>
            </div>
          </li>
        ))}
      </ul>
      <ErrorText error={toggle.error ?? remove.error} />
      {adding && <ScheduleForm serviceId={serviceId} targets={targets} onDone={() => setAdding(false)} />}
    </div>
  );
}

function ScheduleForm({
  serviceId,
  targets,
  onDone,
}: {
  serviceId: string;
  targets: BackupTarget[];
  onDone: () => void;
}) {
  const qc = useQueryClient();
  const [preset, setPreset] = useState(presets[1][0]);
  const [custom, setCustom] = useState("");
  const [form, setForm] = useState({ targetId: "local", keepLast: "0", keepDaily: "7", keepWeekly: "4", keepMonthly: "6" });
  const [verify, setVerify] = useState(true);
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });

  const create = useMutation({
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
      onDone();
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    create.mutate();
  };

  return (
    <form onSubmit={onSubmit} className="space-y-3 rounded-md border border-zinc-200 p-3 dark:border-zinc-800">
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="When">
          <Select value={preset} onChange={(e) => setPreset(e.target.value)}>
            {presets.map(([cron, label]) => (
              <option key={cron} value={cron}>
                {label}
              </option>
            ))}
            <option value="custom">Custom cron…</option>
          </Select>
        </Field>
        <Field label="Target">
          <Select value={form.targetId} onChange={set("targetId")}>
            {targets.map((t) => (
              <option key={t.id} value={t.id}>
                {t.name}
              </option>
            ))}
          </Select>
        </Field>
        {preset === "custom" && (
          <div className="sm:col-span-2">
            <Field label="Cron expression" hint="5 fields, UTC. Prefix with CRON_TZ=Europe/Paris for another zone.">
              <Input required value={custom} onChange={(e) => setCustom(e.target.value)} placeholder="30 2 * * 1-5" />
            </Field>
          </div>
        )}
      </div>
      <div className="grid grid-cols-4 gap-3">
        <Field label="Keep last">
          <Input type="number" min={0} value={form.keepLast} onChange={set("keepLast")} />
        </Field>
        <Field label="Daily">
          <Input type="number" min={0} value={form.keepDaily} onChange={set("keepDaily")} />
        </Field>
        <Field label="Weekly">
          <Input type="number" min={0} value={form.keepWeekly} onChange={set("keepWeekly")} />
        </Field>
        <Field label="Monthly">
          <Input type="number" min={0} value={form.keepMonthly} onChange={set("keepMonthly")} />
        </Field>
      </div>
      <label className="flex items-start gap-2 text-sm">
        <input type="checkbox" className="mt-1" checked={verify} onChange={(e) => setVerify(e.target.checked)} />
        <span>
          Restore-test every backup
          <span className="block text-xs text-zinc-500">
            Restores it into a throwaway, network-less PostgreSQL container and checks the result. A backup you have
            never restored is a hope, not a backup.
          </span>
        </span>
      </label>
      <p className="text-xs text-zinc-500">
        After each run, older backups from this schedule are deleted unless a rule keeps them (newest of each day,
        week, month). All zeros keeps everything. Manual backups are never pruned.
      </p>
      <div className="flex items-center gap-3">
        <Button disabled={create.isPending}>Add schedule</Button>
        <Button type="button" variant="secondary" onClick={onDone}>
          Cancel
        </Button>
        <ErrorText error={create.error} />
      </div>
    </form>
  );
}
