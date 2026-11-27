import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useId, useState, type FormEvent } from "react";
import { toast } from "sonner";
import { Mono } from "@/components/common";
import { CopyField } from "@kipit-labs/kipitidy/copy-button";
import { Section } from "@kipit-labs/kipitidy/section";
import { Button } from "@kipit-labs/kipitidy/button";
import { FloatingInput } from "@kipit-labs/kipitidy/floating-input";
import { Label } from "@kipit-labs/kipitidy/label";
import { Switch } from "@kipit-labs/kipitidy/switch";
import { api, type Service as ServiceT } from "../../../api";
import { formatDateTime } from "@/lib/format";
import { Applies } from "./parts";

/** Whether the analytics server counts the domain's visits. */
export function AnalyticsCard({ svc }: { svc: ServiceT }) {
  const qc = useQueryClient();
  const id = useId();
  const save = useMutation({
    meta: { error: "Couldn't change analytics" },
    mutationFn: (analytics: boolean) => api.updateService(svc.id, { analytics }),
    onSuccess: (updated) => qc.setQueryData(["service", svc.id], updated),
  });
  return (
    <Section
      title={<Applies title="Analytics" when="now" />}
      description={
        <>
          Counts visits of <Mono>{svc.domain}</Mono> without cookies, once analytics are on in Settings › Analytics. The pages need the
          script tag shown in the Analytics tab.
        </>
      }
    >
      <div className="flex items-center gap-3">
        <Switch id={id} checked={svc.analytics} disabled={save.isPending} onCheckedChange={(v) => save.mutate(v)} />
        <Label htmlFor={id}>Count visits</Label>
      </div>
      {svc.analytics && <TrackerPaths svc={svc} />}
      {svc.analytics && <ShareLink svc={svc} />}
    </Section>
  );
}

/** Whether the analytics server takes the errors of the app's Sentry SDK. */
export function ErrorsCard({ svc }: { svc: ServiceT }) {
  const qc = useQueryClient();
  const id = useId();
  const save = useMutation({
    meta: { error: "Couldn't change error tracking" },
    mutationFn: (errors: boolean) => api.updateService(svc.id, { errors }),
    onSuccess: (updated) => qc.setQueryData(["service", svc.id], updated),
  });
  return (
    <Section
      title={<Applies title="Error tracking" when="deploy" />}
      description={
        <>
          Takes the errors the app reports with a Sentry SDK, once analytics are on in Settings › Analytics: each deploy sets{" "}
          <Mono>SENTRY_DSN</Mono>, <Mono>SENTRY_RELEASE</Mono> and <Mono>SENTRY_ENVIRONMENT</Mono>. Issues show in the Errors tab.
        </>
      }
    >
      <div className="flex items-center gap-3">
        <Switch id={id} checked={svc.errors} disabled={save.isPending} onCheckedChange={(v) => save.mutate(v)} />
        <Label htmlFor={id}>Track errors</Label>
      </div>
    </Section>
  );
}

/** The app's own tracker paths on its domain, which ad blockers don't know, instead of the analytics domain's /a.js and /e. */
function TrackerPaths({ svc }: { svc: ServiceT }) {
  const qc = useQueryClient();
  const saved = { script: svc.analyticsScript ?? "", hit: svc.analyticsHit ?? "" };
  const [form, setForm] = useState(saved);
  const dirty = form.script !== saved.script || form.hit !== saved.hit;
  const save = useMutation({
    meta: { error: "Couldn't change the tracker paths" },
    mutationFn: (p: { script: string; hit: string }) => api.updateService(svc.id, { analyticsScript: p.script, analyticsHit: p.hit }),
    onSuccess: (updated) => {
      qc.setQueryData(["service", svc.id], updated);
      setForm({ script: updated.analyticsScript ?? "", hit: updated.analyticsHit ?? "" });
      toast.success("Tracker paths saved");
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate({ script: form.script.trim(), hit: form.hit.trim() });
  };
  return (
    <form className="space-y-3 border-t pt-4" onSubmit={onSubmit}>
      <div>
        <p className="text-sm font-medium">Tracker paths</p>
        <p className="text-sm text-muted-foreground">
          Ad blockers know the analytics domain's <Mono>/a.js</Mono> and <Mono>/e</Mono>. Paths of your own serve the script and take the
          visits on <Mono>{svc.domain}</Mono> itself, without a deploy; leave both empty for the defaults. The old tag keeps counting until you
          change it.
        </p>
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <FloatingInput label="Script path, e.g. /js/app.js" value={form.script} onChange={(e) => setForm({ ...form, script: e.target.value })} />
        <FloatingInput label="Hits path, e.g. /api/collect" value={form.hit} onChange={(e) => setForm({ ...form, hit: e.target.value })} />
      </div>
      {saved.script && (
        <div className="space-y-1.5">
          <p className="text-xs text-muted-foreground">The tag for the app's pages:</p>
          <CopyField value={`<script defer src="https://${svc.domain}${saved.script}"></script>`} />
        </div>
      )}
      <div className="flex gap-2">
        <Button type="submit" variant="outline" size="sm" disabled={!dirty || save.isPending}>
          Save paths
        </Button>
        {saved.script && (
          <Button type="button" variant="ghost" size="sm" disabled={save.isPending} onClick={() => save.mutate({ script: "", hit: "" })}>
            Use the defaults
          </Button>
        )}
      </div>
    </form>
  );
}

/** The app's public, read-only Analytics tab: one link at a time, its token shown once. */
function ShareLink({ svc }: { svc: ServiceT }) {
  const qc = useQueryClient();
  const [token, setToken] = useState("");
  const refresh = () => qc.invalidateQueries({ queryKey: ["service", svc.id] });
  const share = useMutation({
    meta: { error: "Couldn't create the link" },
    mutationFn: () => api.shareAnalytics(svc.id),
    onSuccess: (res) => (setToken(res.token), refresh()),
  });
  const revoke = useMutation({
    meta: { error: "Couldn't revoke the link" },
    mutationFn: () => api.unshareAnalytics(svc.id),
    onSuccess: () => (setToken(""), refresh(), toast.success("Link revoked")),
  });
  const busy = share.isPending || revoke.isPending;
  return (
    <div className="space-y-3 border-t pt-4">
      <div>
        <p className="text-sm font-medium">Public link</p>
        <p className="text-sm text-muted-foreground">
          Anyone with it sees the Analytics tab without signing in: the numbers, lists and range picker, never the visits, identified users,
          event properties or settings.
        </p>
      </div>
      {token && (
        <div className="space-y-1.5">
          <CopyField value={`${location.origin}${import.meta.env.BASE_URL.replace(/\/$/, "")}/share/analytics/${token}`} />
          <p className="text-xs text-muted-foreground">Copy it now: it isn't shown again.</p>
        </div>
      )}
      <div className="flex flex-wrap items-center gap-2">
        {svc.analyticsSharedAt && !token && (
          <span className="text-sm text-muted-foreground">Shared since {formatDateTime(svc.analyticsSharedAt)}.</span>
        )}
        <Button variant="outline" size="sm" disabled={busy} onClick={() => share.mutate()}>
          {svc.analyticsSharedAt ? "New link" : "Create link"}
        </Button>
        {svc.analyticsSharedAt && (
          <Button variant="outline" size="sm" disabled={busy} onClick={() => revoke.mutate()}>
            Revoke
          </Button>
        )}
      </div>
      {svc.analyticsSharedAt && <p className="text-xs text-muted-foreground">A new link revokes the current one.</p>}
    </div>
  );
}
