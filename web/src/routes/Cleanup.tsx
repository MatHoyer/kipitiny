import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, type FormEvent } from "react";
import { CheckboxField, ErrorText, Mono, Section } from "@/components/common";
import { Button } from "@/components/ui/button";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingSelect } from "@/components/ui/floating-select";
import { formatBytes, timeAgo } from "@/lib/format";
import { api, type Cleanup as CleanupState, type CleanupResult, type CleanupSettings } from "../api";

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
  const save = useMutation({ mutationFn: (s: CleanupSettings) => api.setCleanup(s), onSuccess: onSaved });
  const run = useMutation({ mutationFn: api.runCleanup, onSuccess: (v) => qc.setQueryData(["cleanup"], v) });

  if (!form) return null;
  const set = <K extends keyof CleanupSettings>(k: K, v: CleanupSettings[K]) => setForm({ ...form, [k]: v });
  const dirty = JSON.stringify(form) !== JSON.stringify(cleanup.data?.settings);
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate(form);
  };

  return (
    <Section
      title="Cleanup"
      description="Frees disk space on every server by removing what Docker leaves behind. Database volumes, the images of your services and of their recent deployments (for rollback), and everything kipitiny runs are always kept."
      actions={
        <Button
          variant="outline"
          size="sm"
          disabled={cleanup.data?.running || run.isPending || dirty}
          title={dirty ? "Save first" : undefined}
          onClick={() => run.mutate()}
        >
          {cleanup.data?.running ? "Cleaning…" : "Run now"}
        </Button>
      }
    >
      <form onSubmit={onSubmit} className="space-y-4">
        <CheckboxField
          label="Run on a schedule"
          description={cleanup.data?.nextRun && `Next run ${new Date(cleanup.data.nextRun).toLocaleString()}.`}
          checked={form.enabled}
          onCheckedChange={(v) => set("enabled", v)}
        />
        <div className="grid gap-4 sm:grid-cols-2">
          <FloatingInput
            label="Cron expression"
            required
            value={form.cron}
            onChange={(e) => set("cron", e.target.value)}
            inputClassName="font-mono"
            description={
              <>
                5 fields, UTC. Prefix with <Mono>CRON_TZ=Europe/Paris</Mono> for another zone.
              </>
            }
          />
          <FloatingInput
            label="Only older than (hours)"
            type="number"
            min={1}
            required
            value={String(form.minAgeHours)}
            onChange={(e) => set("minAgeHours", Number(e.target.value) || 0)}
            description="Leaves recent leftovers alone, e.g. an image a deploy just built."
          />
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
        <div className="grid gap-3 sm:grid-cols-3">
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
        <ErrorText error={save.error ?? run.error} />
        {dirty && (
          <div className="flex gap-2">
            <Button type="submit" size="sm" disabled={save.isPending}>
              Save
            </Button>
            <Button type="button" size="sm" variant="ghost" onClick={() => setForm(cleanup.data!.settings)}>
              Cancel
            </Button>
          </div>
        )}
      </form>
      {cleanup.data?.lastRun && <LastRun run={cleanup.data.lastRun} />}
    </Section>
  );
}

function LastRun({ run }: { run: NonNullable<CleanupState["lastRun"]> }) {
  const freed = run.servers.reduce((n, s) => n + s.reclaimed, 0);
  return (
    <div className="space-y-1 border-t pt-4 text-sm">
      <p>
        Last run {timeAgo(run.startedAt)} ({run.trigger})
        {run.finishedAt ? <> · ≈ {formatBytes(freed)} freed</> : " · running"}
        {run.deployments > 0 && <> · {run.deployments} old deployments removed</>}
      </p>
      {run.error && <p className="text-destructive">{run.error}</p>}
      <ul className="text-xs text-muted-foreground">
        {run.servers.map((s) => (
          <li key={s.server}>
            <span className="font-medium text-foreground">{s.server}</span>: {describe(s)}
            {s.errors?.map((e) => (
              <span key={e} className="block text-destructive">
                {e}
              </span>
            ))}
          </li>
        ))}
      </ul>
    </div>
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
  return parts.length ? `${parts.join(", ")} (≈ ${formatBytes(s.reclaimed)})` : "nothing to remove";
}
