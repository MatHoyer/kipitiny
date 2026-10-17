import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChartNoAxesColumn, Power, Trash2 } from "lucide-react";
import { useEffect, useId, useState, type FormEvent } from "react";
import { toast } from "sonner";
import { CopyField, ErrorText, Loading, Mono, Section } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { SaveBar } from "@/components/save-bar";
import { StepsProgress, type PlannedStep } from "@/components/test-dialog";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { api, type AnalyticsSettings, type AnalyticsStep } from "@/api";
import { SettingsPage } from "./page";

/** What turning analytics on does, as the manager reports it. */
const enablePlan: (PlannedStep & { id: AnalyticsStep })[] = [
  { id: "image", title: "Download the analytics server", hint: "A small image, the first time only", skipped: "Already downloaded" },
  { id: "start", title: "Start its container", hint: "Next to Traefik, with its own volume for the data", skipped: "Already running" },
  { id: "health", title: "Wait until it answers", hint: "Its domain is then served over HTTPS", skipped: "Already running" },
  { id: "sites", title: "Tell it which apps to count", hint: "The apps whose Analytics switch is on" },
];

export function Analytics() {
  const qc = useQueryClient();
  const settings = useQuery({ queryKey: ["analytics"], queryFn: api.analytics });
  const [form, setForm] = useState<AnalyticsSettings | null>(null);
  const [enabling, setEnabling] = useState(false);
  useEffect(() => {
    if (settings.data && !form) setForm(settings.data);
  }, [settings.data, form]);
  const onSaved = (v: AnalyticsSettings) => {
    qc.setQueryData(["analytics"], v);
    setForm(v);
  };
  const save = useMutation({ meta: { error: "Couldn't save the analytics settings" }, mutationFn: (s: AnalyticsSettings) => api.setAnalytics(s), onSuccess: onSaved });
  const disable = useMutation({
    meta: { error: "Couldn't turn analytics off" },
    mutationFn: () => api.setAnalytics({ ...settings.data!, enabled: false }),
    onSuccess: (v) => {
      onSaved(v);
      toast.success("Analytics turned off", { description: "The collected data is kept." });
    },
  });
  const wipe = useMutation({
    meta: { error: "Couldn't delete the analytics data" },
    mutationFn: api.deleteAnalyticsData,
    onSuccess: () => toast.success("Analytics data deleted"),
  });
  const formId = useId();
  const data = settings.data;
  if (!form || !data) return <SettingsPage>{settings.error ? <ErrorText error={settings.error} /> : <Loading />}</SettingsPage>;

  const actions = data.enabled ? (
    <ConfirmDialog
      trigger={
        <Button variant="outline" size="sm" loading={disable.isPending}>
          <Power data-icon="inline-start" />
          Disable
        </Button>
      }
      title="Turn analytics off?"
      description="The analytics server stops and visits are no longer counted. What was collected is kept for when you turn it back on."
      confirmLabel="Disable"
      destructive={false}
      onConfirm={() => disable.mutate()}
    />
  ) : (
    <Button size="sm" onClick={() => setEnabling(true)}>
      <ChartNoAxesColumn data-icon="inline-start" />
      Enable analytics
    </Button>
  );
  const dirty = JSON.stringify(form) !== JSON.stringify(data);
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate(form);
  };
  return (
    <SettingsPage actions={actions}>
      {enabling && <EnableDialog initial={data} onClose={() => setEnabling(false)} onEnabled={onSaved} />}
      {data.enabled ? (
        <>
          <form id={formId} onSubmit={onSubmit}>
            <Section
              title="Web analytics"
              description="On: visits of the apps that opted in are counted without cookies, by a small server next to Traefik. The data stays in its own volume on this server."
            >
              <SettingsFields value={form} onChange={setForm} />
              <div className="space-y-1.5">
                <p className="text-sm">
                  Turn on <Mono>Analytics</Mono> in an app's networking settings (or <Mono>x-kipitiny.analytics: true</Mono>), then add to its
                  pages:
                </p>
                <CopyField value={`<script defer src="https://${data.domain}/a.js"></script>`} />
              </div>
            </Section>
          </form>
          <SaveBar form={formId} dirty={dirty} saving={save.isPending} onReset={() => setForm(data)} />
        </>
      ) : (
        <>
          <Section
            title="Web analytics"
            description="Pageviews, visitors and where they come from, counted without cookies or a consent banner by a small server kipitiny runs next to Traefik. Nothing goes to a third party: the data stays in its own volume on this server."
          >
            <p className="text-sm text-muted-foreground">Off. Enable it from the button above: you'll see what happens before it starts.</p>
          </Section>
          <Section title="Collected data" description="Turning analytics off keeps what was collected. Deleting it can't be undone.">
            <ConfirmDialog
              trigger={
                <Button variant="outline" size="sm" className="w-fit" loading={wipe.isPending}>
                  <Trash2 data-icon="inline-start" />
                  Delete analytics data
                </Button>
              }
              title="Delete all analytics data?"
              description="Every app's visits are deleted from this server."
              onConfirm={() => wipe.mutate()}
            />
          </Section>
        </>
      )}
    </SettingsPage>
  );
}

function SettingsFields({ value, onChange, disabled }: { value: AnalyticsSettings; onChange: (v: AnalyticsSettings) => void; disabled?: boolean }) {
  return (
    <div className="grid gap-4 sm:grid-cols-2">
      <FloatingInput
        label="Domain"
        placeholder="stats.example.com"
        required
        disabled={disabled}
        value={value.domain}
        onChange={(e) => onChange({ ...value, domain: e.target.value })}
        description="Serves the tracker script and receives visits. Point its DNS at this server (synced with Cloudflare connected)."
      />
      <FloatingInput
        label="Keep (days)"
        type="number"
        min={1}
        max={3650}
        disabled={disabled}
        value={String(value.retentionDays)}
        onChange={(e) => onChange({ ...value, retentionDays: Number(e.target.value) || 0 })}
        description="Older visits are deleted."
      />
    </div>
  );
}

/** Asks for the domain, shows the steps turning analytics on takes, and runs them on Enable. */
function EnableDialog({ initial, onClose, onEnabled }: { initial: AnalyticsSettings; onClose: () => void; onEnabled: (v: AnalyticsSettings) => void }) {
  const [form, setForm] = useState(initial);
  const [seen, setSeen] = useState<AnalyticsStep[]>([]);
  const enable = useMutation({
    // The steps show where it failed instead.
    meta: { error: false },
    mutationFn: () => {
      setSeen([]);
      return api.setAnalytics({ ...form, enabled: true }, (s) => setSeen((prev) => [...prev, s]));
    },
  });
  const close = () => {
    if (enable.isPending) return;
    // The page swaps to its "on" view only now, so the steps stay on screen until Done.
    if (enable.isSuccess) onEnabled(enable.data);
    onClose();
  };
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    enable.mutate();
  };
  const started = enable.isPending || enable.isSuccess || enable.isError;
  return (
    <Dialog open onOpenChange={(next) => !next && close()}>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={onSubmit} className="contents">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2 [&>svg]:size-5">
              <ChartNoAxesColumn />
              Enable analytics
            </DialogTitle>
            <DialogDescription>Cookieless visit counting for the apps you pick, kept on this server.</DialogDescription>
          </DialogHeader>
          <SettingsFields value={form} onChange={setForm} disabled={started && !enable.isError} />
          <div className="grid gap-2.5 rounded-lg border bg-muted/30 p-3">
            <p className="text-xs font-medium text-muted-foreground">
              {enable.isSuccess ? "Analytics are on." : enable.isPending ? "Enabling…" : enable.isError ? "Enabling failed." : "What happens:"}
            </p>
            <StepsProgress plan={enablePlan} seen={seen} running={enable.isPending} done={enable.isSuccess} error={enable.error} />
          </div>
          <DialogFooter>
            {enable.isSuccess ? (
              <Button type="button" onClick={close}>
                Done
              </Button>
            ) : (
              <>
                <Button type="button" variant="ghost" disabled={enable.isPending} onClick={close}>
                  Cancel
                </Button>
                <Button type="submit" loading={enable.isPending}>
                  {enable.isError ? "Try again" : "Enable"}
                </Button>
              </>
            )}
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
