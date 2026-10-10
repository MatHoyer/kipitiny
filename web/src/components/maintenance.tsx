import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Code, ExternalLink, Type } from "lucide-react";
import { lazy, Suspense, useId, useState, type FormEvent } from "react";
import { toast } from "sonner";
import { CheckboxField, Mono, Section } from "@/components/common";
import { SaveBar } from "@/components/save-bar";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingTextarea } from "@/components/ui/floating-textarea";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { api, type Maintenance, type Service } from "../api";

// CodeMirror and its HTML, CSS and JS modes load only for custom pages.
const HtmlEditor = lazy(() => import("./html-editor"));

type Form = { enabled: boolean; allow: string; custom: boolean; title: string; message: string; html: string };

/** The default page's text, as the manager renders it (internal/core/maintenance.go). */
const defaults = (name: string) => ({
  title: "We'll be back soon",
  on: `${name} is down for maintenance. Please check back in a few minutes.`,
  off: `${name} is unavailable right now. Please try again in a few minutes.`,
});
type Defaults = ReturnType<typeof defaults>;

// The fields show the default text; left as is, it is saved empty so the
// message keeps following maintenance mode.
const toForm = (m: Maintenance | undefined, d: Defaults): Form => ({
  enabled: !!m?.enabled,
  allow: (m?.allowIps ?? []).join("\n"),
  custom: !!m?.html,
  title: m?.title || d.title,
  message: m?.message || (m?.enabled ? d.on : d.off),
  html: m?.html ?? "",
});

const toInput = (f: Form, d: Defaults): Required<Maintenance> => ({
  enabled: f.enabled,
  allowIps: f.allow
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean),
  title: f.title.trim() === d.title ? "" : f.title,
  message: [d.on, d.off].includes(f.message.trim()) ? "" : f.message,
  // The default page is the one without HTML.
  html: f.custom ? f.html : "",
});

const htmlStarter = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Back soon</title>
</head>
<body>
  <h1>We'll be back soon</h1>
</body>
</html>`;

/** Shows the page visitors get while the app can't answer, and turns maintenance mode on. */
export function MaintenanceCard({ svc, projectName }: { svc: Service; projectName: string }) {
  const qc = useQueryClient();
  const d = defaults(projectName || svc.name);
  const [form, setForm] = useState(() => toForm(svc.maintenance, d));
  const dirty = JSON.stringify(toInput(form, d)) !== JSON.stringify(toInput(toForm(svc.maintenance, d), d));
  const formId = useId();
  const save = useMutation({
    meta: { error: "Couldn't save the maintenance page" },
    mutationFn: () => api.setMaintenance(svc.id, toInput(form, d)),
    onSuccess: (updated) => {
      qc.setQueryData(["service", svc.id], updated);
      setForm(toForm(updated.maintenance, d));
      const was = !!svc.maintenance?.enabled;
      toast.success(updated.maintenance.enabled === was ? "Maintenance page saved" : updated.maintenance.enabled ? "Maintenance mode on" : "Maintenance mode off", {
        description: "Traefik applies it within seconds; nothing restarted.",
      });
    },
  });
  // Custom HTML starts from the page visitors get now, the saved default one.
  const pick = (custom: boolean) => {
    setForm((f) => ({ ...f, custom }));
    if (!custom || form.html) return;
    fetch(`/api/pages/${svc.id}`)
      .then((r) => (r.headers.get("Content-Type")?.startsWith("text/html") ? r.text() : htmlStarter))
      .catch(() => htmlStarter)
      .then((html) => setForm((f) => (f.custom && !f.html ? { ...f, html } : f)));
  };
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };
  return (
    <Section
      title="Maintenance"
      description={
        <>
          Visitors of <Mono>{svc.domain}</Mono> get this page (503, so search engines come back later) while no replica can answer: stopped,
          crashed or not ready yet.
        </>
      }
    >
      <form id={formId} onSubmit={onSubmit} className="space-y-6">
        <div className="space-y-3">
          <CheckboxField
            label="Maintenance mode"
            description="Every visitor gets the page while the replicas keep running, e.g. during a migration. Nothing restarts."
            checked={form.enabled}
            // The default message follows the mode; a custom one stays.
            onCheckedChange={(enabled) =>
              setForm({ ...form, enabled, message: form.message === (enabled ? d.off : d.on) ? (enabled ? d.on : d.off) : form.message })
            }
          />
          {form.enabled && (
            <FloatingTextarea
              label="Still reach the app"
              value={form.allow}
              onChange={(e) => setForm({ ...form, allow: e.target.value })}
              placeholder={"203.0.113.7\n10.0.0.0/8"}
              className="[&_textarea]:font-mono"
              description="One IP or CIDR range per line, e.g. your own, to check the app before reopening it. They still go through the app's access settings."
            />
          )}
        </div>

        <div className="space-y-3">
          <ToggleGroup
            type="single"
            variant="outline"
            size="sm"
            spacing={0}
            value={form.custom ? "html" : "default"}
            onValueChange={(v) => v && pick(v === "html")}
            aria-label="Page"
          >
            <ToggleGroupItem value="default" className="aria-checked:bg-muted">
              <Type />
              Default page
            </ToggleGroupItem>
            <ToggleGroupItem value="html" className="aria-checked:bg-muted">
              <Code />
              Custom HTML
            </ToggleGroupItem>
          </ToggleGroup>
          {form.custom ? (
            <div className="space-y-1.5">
              <Suspense fallback={<div className="h-64 rounded-md border" />}>
                <HtmlEditor label="Maintenance page HTML" value={form.html} onChange={(html) => setForm((f) => ({ ...f, html }))} />
              </Suspense>
              <p className="text-xs text-muted-foreground">
                A whole document, up to 64 KiB. Inline its styles and images, or load them from another site: the app is down.
              </p>
            </div>
          ) : (
            <>
              <FloatingInput
                label="Title"
                value={form.title}
                maxLength={200}
                onChange={(e) => setForm({ ...form, title: e.target.value })}
              />
              <FloatingTextarea
                label="Message"
                value={form.message}
                maxLength={2000}
                onChange={(e) => setForm({ ...form, message: e.target.value })}
                description="The default text follows maintenance mode until you change it."
              />
            </>
          )}
          <a
            href={`/api/pages/${svc.id}`}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1 text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
          >
            Open the saved page
            <ExternalLink className="size-3.5" />
          </a>
        </div>
      </form>
      <SaveBar form={formId} dirty={dirty} saving={save.isPending} onReset={() => setForm(toForm(svc.maintenance, d))} label="Save maintenance" />
    </Section>
  );
}
