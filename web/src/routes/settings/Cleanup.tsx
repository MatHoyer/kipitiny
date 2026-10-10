import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CalendarClock, HardDrive, History, Play, Server, Trash2 } from "lucide-react";
import { useEffect, useId, useState, type FormEvent } from "react";
import { CheckboxField, ErrorText, Loading, Section, StatCard } from "@/components/common";
import { CronField, cleanupPresets } from "@/components/cron-field";
import { SaveBar } from "@/components/save-bar";
import { Button } from "@/components/ui/button";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingSelect } from "@/components/ui/floating-select";
import { formatBytes, formatDateTime, timeAgo } from "@/lib/format";
import { api, type Cleanup as CleanupState, type CleanupResult, type CleanupSettings } from "@/api";
import { SettingsPage } from "./page";

const imageModes = [
  { value: "off", label: "Keep all" },
  { value: "dangling", label: "Dangling (untagged layers)" },
  { value: "unused", label: "All unused" },
];

const volumeModes = [
  { value: "off", label: "Keep all" },
  { value: "anonymous", label: "Unused anonymous" },
  { value: "unused", label: "All unused, named too" },
];

export function Cleanup() {
  const qc = useQueryClient();
  const cleanup = useQuery({
    queryKey: ["cleanup"],
    queryFn: api.cleanup,
    refetchInterval: (q) => (q.state.data?.running ? 2000 : false),
  });
  const [form, setForm] = useState<CleanupSettings | null>(null);
  useEffect(() => {
    if (cleanup.data && !form) setForm(cleanup.data.settings);
  }, [cleanup.data, form]);

  const onSaved = (v: CleanupState) => {
    qc.setQueryData(["cleanup"], v);
    setForm(v.settings);
  };
  const save = useMutation({ meta: { error: "Couldn't save the cleanup settings" }, mutationFn: (s: CleanupSettings) => api.setCleanup(s), onSuccess: onSaved });
  const run = useMutation({ meta: { error: "Couldn't run the cleanup" }, mutationFn: api.runCleanup, onSuccess: (v) => qc.setQueryData(["cleanup"], v) });

  const formId = useId();
  const dirty = !!form && JSON.stringify(form) !== JSON.stringify(cleanup.data?.settings);
  const running = cleanup.data?.running || run.isPending;
  const actions = (
    <Button variant="outline" size="sm" loading={running} disabled={!form || dirty} title={dirty ? "Save first" : undefined} onClick={() => run.mutate()}>
      <Play data-icon="inline-start" />
      {cleanup.data?.running ? "Cleaning" : "Run now"}
    </Button>
  );
  if (!form)
    return (
      <SettingsPage actions={actions}>
        {cleanup.error ? <ErrorText error={cleanup.error} /> : <Loading />}
      </SettingsPage>
    );

  const set = <K extends keyof CleanupSettings>(k: K, v: CleanupSettings[K]) => setForm({ ...form, [k]: v });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate(form);
  };
  const last = cleanup.data?.lastRun;

  return (
    <SettingsPage actions={actions}>
      <CleanupStats state={cleanup.data!} />
      <form id={formId} onSubmit={onSubmit} className="space-y-6">
        <Section title="Schedule" description="When the cleanup runs on its own, on every server.">
          <CheckboxField
            label="Run on a schedule"
            description={cleanup.data?.nextRun && `Next run ${formatDateTime(cleanup.data.nextRun)}.`}
            checked={form.enabled}
            onCheckedChange={(v) => set("enabled", v)}
          />
          <div className="grid gap-4 sm:grid-cols-2">
            <CronField value={form.cron} onChange={(v) => set("cron", v)} presets={cleanupPresets} inputClassName="sm:order-last" />
            <FloatingInput
              label="Only older than (hours)"
              type="number"
              min={1}
              required
              value={String(form.minAgeHours)}
              onChange={(e) => set("minAgeHours", Number(e.target.value) || 0)}
              description="Leaves recent leftovers alone, e.g. an image a deploy just built."
            />
          </div>
        </Section>
        <Section
          title="What to remove"
          description="Database volumes, the images of your services and of their recent deployments (for rollback), and everything kipitiny runs are always kept."
        >
          <div className="grid gap-4 sm:grid-cols-2">
            <FloatingSelect
              label="Images"
              value={form.images}
              onValueChange={(v) => set("images", v as CleanupSettings["images"])}
              options={imageModes}
              description="Unused images are pulled again if needed."
            />
            <FloatingSelect
              label="Volumes"
              value={form.volumes}
              onValueChange={(v) => set("volumes", v as CleanupSettings["volumes"])}
              options={volumeModes}
              description={
                form.volumes === "unused" ? (
                  <span className="text-destructive">
                    Deletes the data of any stopped stack on the host that isn't managed by kipitiny.
                  </span>
                ) : (
                  "Volumes no container mounts."
                )
              }
            />
            <FloatingInput
              label="Deployments kept per service"
              type="number"
              min={0}
              value={String(form.keepDeployments)}
              onChange={(e) => set("keepDeployments", Number(e.target.value) || 0)}
              description="Older history and build logs are deleted. 0 keeps all, otherwise at least 10."
            />
          </div>
          <div className="grid gap-4 sm:grid-cols-3">
            <CheckboxField
              label="Build cache"
              description="Layers cached by Git builds."
              checked={form.buildCache}
              onCheckedChange={(v) => set("buildCache", v)}
            />
            <CheckboxField
              label="Stopped containers"
              description="Only ones kipitiny didn't create."
              checked={form.containers}
              onCheckedChange={(v) => set("containers", v)}
            />
            <CheckboxField
              label="Unused networks"
              description="Only ones kipitiny didn't create."
              checked={form.networks}
              onCheckedChange={(v) => set("networks", v)}
            />
          </div>
        </Section>
      </form>
      <SaveBar form={formId} dirty={dirty} saving={save.isPending} onReset={() => setForm(cleanup.data!.settings)} />
      {last && <LastRun run={last} />}
    </SettingsPage>
  );
}

function CleanupStats({ state }: { state: CleanupState }) {
  const run = state.lastRun;
  const freed = run?.servers.reduce((n, s) => n + s.reclaimed, 0) ?? 0;
  const removed = run?.servers.reduce((n, s) => n + s.images + s.volumes + s.containers + s.networks + s.buildCache, 0) ?? 0;
  const failed = !!run?.error || !!run?.servers.some((s) => s.errors?.length);
  return (
    <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
      <StatCard
        icon={CalendarClock}
        label="Schedule"
        value={state.settings.enabled ? "On" : "Off"}
        hint={state.settings.enabled && state.nextRun ? `Next ${formatDateTime(state.nextRun)}` : "Runs only on demand"}
      />
      <StatCard
        icon={History}
        label="Last run"
        value={run ? timeAgo(run.startedAt) : "never"}
        hint={run && (run.finishedAt ? run.trigger : "running")}
        tone={failed ? "bad" : undefined}
      />
      <StatCard icon={HardDrive} label="Freed" value={run?.finishedAt ? `≈ ${formatBytes(freed)}` : "—"} hint="Last run" />
      <StatCard
        icon={Trash2}
        label="Removed"
        value={run?.finishedAt ? removed : "—"}
        hint={run && run.deployments > 0 ? `and ${run.deployments} old deployments` : "Docker objects"}
      />
    </div>
  );
}

function LastRun({ run }: { run: NonNullable<CleanupState["lastRun"]> }) {
  return (
    <Section
      title="Last run"
      description={`${run.trigger === "manual" ? "Run by hand" : "Scheduled"}, ${timeAgo(run.startedAt)}${run.finishedAt ? "" : ", still running"}.`}
    >
      {run.error && <p className="text-sm text-destructive">{run.error}</p>}
      <ul className="divide-y">
        {run.servers.map((s) => (
          <li key={s.server} className="flex items-start gap-3 py-2.5 first:pt-0 last:pb-0">
            <Server className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
            <div className="min-w-0 flex-1 text-sm">
              <p className="font-medium">{s.server}</p>
              <p className="text-xs text-muted-foreground">{describe(s)}</p>
              {s.errors?.map((e) => (
                <p key={e} className="text-xs text-destructive">
                  {e}
                </p>
              ))}
            </div>
            {s.reclaimed > 0 && <span className="text-xs text-muted-foreground tabular-nums">≈ {formatBytes(s.reclaimed)}</span>}
          </li>
        ))}
      </ul>
    </Section>
  );
}

function describe(s: CleanupResult): string {
  const counts: [number, string, string][] = [
    [s.images, "image", "images"],
    [s.volumes, "volume", "volumes"],
    [s.containers, "container", "containers"],
    [s.networks, "network", "networks"],
    [s.buildCache, "cache entry", "cache entries"],
  ];
  const parts = counts.filter(([n]) => n > 0).map(([n, one, many]) => `${n} ${n === 1 ? one : many}`);
  return parts.length ? parts.join(", ") : "nothing to remove";
}
