import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Plus, Trash2 } from "lucide-react";
import { useId, useState, type FormEvent } from "react";
import { toast } from "sonner";
import { Mono, Section } from "@/components/common";
import { BasicAuthUsers, type BasicAuthRow } from "@/components/basic-auth-users";
import { SaveBar } from "@/components/save-bar";
import { Button } from "@/components/ui/button";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingTextarea } from "@/components/ui/floating-textarea";
import { api, type Middlewares, type MiddlewaresInput, type Service as ServiceT } from "../../api";
import { Applies } from "./parts";

type AccessForm = {
  users: BasicAuthRow[];
  allow: string;
  average: string;
  burst: string;
  headers: { name: string; value: string }[];
};

function accessForm(m: Middlewares): AccessForm {
  return {
    users: (m.basicAuth ?? []).map((u) => ({ name: u.name, password: u.ref ?? "", stored: true })),
    allow: (m.ipAllowList ?? []).join("\n"),
    average: m.rateLimit ? String(m.rateLimit.average) : "",
    burst: m.rateLimit ? String(m.rateLimit.burst) : "",
    headers: Object.entries(m.headers ?? {})
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([name, value]) => ({ name, value })),
  };
}

function accessInput(f: AccessForm): MiddlewaresInput {
  const average = Number(f.average);
  return {
    basicAuth: f.users.map((u) => ({ name: u.name.trim(), password: u.password.trim() })),
    ipAllowList: f.allow.split(/[\s,]+/).filter(Boolean),
    rateLimit: f.average.trim() ? { average, burst: f.burst.trim() ? Number(f.burst) : average } : null,
    headers: Object.fromEntries(f.headers.filter((h) => h.name.trim()).map((h) => [h.name.trim(), h.value.trim()])),
  };
}

/** Traefik middlewares on the public domain: basic auth, IP allowlist, rate limit, response headers. */
export function AccessCard({ svc }: { svc: ServiceT }) {
  const qc = useQueryClient();
  const [form, setForm] = useState(() => accessForm(svc.middlewares));
  const dirty = JSON.stringify(form) !== JSON.stringify(accessForm(svc.middlewares));
  const formId = useId();
  const save = useMutation({
    meta: { error: "Couldn't save the access settings" },
    mutationFn: () => api.updateService(svc.id, { middlewares: accessInput(form) }),
    onSuccess: (updated) => {
      qc.setQueryData(["service", svc.id], updated);
      setForm(accessForm(updated.middlewares));
      toast.success("Access settings saved", { description: "Deploy to apply." });
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };
  const setHeader = (i: number, patch: Partial<AccessForm["headers"][number]>) =>
    setForm({ ...form, headers: form.headers.map((h, j) => (j === i ? { ...h, ...patch } : h)) });

  return (
    <Section title={<Applies title="Access" when="deploy" />} description={<>Applied by Traefik to every request for <Mono>{svc.domain}</Mono>.</>}>
      <form id={formId} onSubmit={onSubmit} className="space-y-6">
        <BasicAuthUsers rows={form.users} onChange={(users) => setForm({ ...form, users })} />

        <FloatingTextarea
          label="IP allowlist"
          value={form.allow}
          onChange={(e) => setForm({ ...form, allow: e.target.value })}
          placeholder={"203.0.113.7\n10.0.0.0/8"}
          className="[&_textarea]:font-mono"
          description="One IP or CIDR range per line. Empty allows everyone; otherwise other clients get 403."
        />

        <div className="space-y-3">
          <div>
            <h3 className="text-sm font-medium">Rate limit</h3>
            <p className="text-sm text-muted-foreground">Requests per second per client IP; over the limit, clients get 429. Empty means unlimited.</p>
          </div>
          <div className="flex gap-2">
            <FloatingInput
              label="Requests / s"
              type="number"
              min={1}
              value={form.average}
              onChange={(e) => setForm({ ...form, average: e.target.value })}
              className="w-40"
            />
            <FloatingInput
              label="Burst"
              type="number"
              min={1}
              value={form.burst}
              onChange={(e) => setForm({ ...form, burst: e.target.value })}
              placeholder={form.average || "Same as the rate"}
              className="w-40"
              disabled={!form.average.trim()}
            />
          </div>
        </div>

        <div className="space-y-3">
          <div className="flex items-center justify-between gap-2">
            <div>
              <h3 className="text-sm font-medium">Response headers</h3>
              <p className="text-sm text-muted-foreground">
                Set on every response, e.g. <Mono>X-Robots-Tag: noindex</Mono> for staging. An empty value removes the header.
              </p>
            </div>
            <Button type="button" size="sm" variant="outline" onClick={() => setForm({ ...form, headers: [...form.headers, { name: "", value: "" }] })}>
              <Plus data-icon="inline-start" />
              Add header
            </Button>
          </div>
          {form.headers.map((h, i) => (
            <div key={i} className="flex items-start gap-2">
              <FloatingInput
                label="Header"
                required
                value={h.name}
                onChange={(e) => setHeader(i, { name: e.target.value })}
                placeholder="X-Robots-Tag"
                className="w-40 shrink-0 sm:w-56"
                inputClassName="font-mono"
              />
              <FloatingInput
                label="Value"
                value={h.value}
                onChange={(e) => setHeader(i, { value: e.target.value })}
                placeholder="noindex"
                className="min-w-0 flex-1"
                inputClassName="font-mono"
              />
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                title="Remove"
                aria-label={`Remove ${h.name || "header"}`}
                className="mt-3.5 text-muted-foreground hover:text-destructive"
                onClick={() => setForm({ ...form, headers: form.headers.filter((_, j) => j !== i) })}
              >
                <Trash2 />
              </Button>
            </div>
          ))}
        </div>
      </form>
      <SaveBar form={formId} dirty={dirty} saving={save.isPending} onReset={() => setForm(accessForm(svc.middlewares))} label="Save access" />
    </Section>
  );
}
