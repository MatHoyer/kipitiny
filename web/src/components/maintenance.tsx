import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Code, ExternalLink, Type } from "lucide-react";
import { lazy, Suspense, useId, useState, type FormEvent } from "react";
import { toast } from "sonner";
import { CheckboxField, Mono, Section } from "@/components/common";
import { SaveBar } from "@/components/save-bar";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingTextarea } from "@/components/ui/floating-textarea";
import { Segmented, SegmentedItem } from "@/components/ui/segmented";
import { Applies } from "@/components/service-settings/parts";
import { api, type Maintenance, type Project } from "../api";

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
  starting: {},
});

/** The maintenance settings as saved, to change one part of them. */
const saved = (m: Maintenance | undefined): Required<Maintenance> => ({
  enabled: !!m?.enabled,
  allowIps: m?.allowIps ?? [],
  title: m?.title ?? "",
  message: m?.message ?? "",
  html: m?.html ?? "",
  starting: m?.starting ?? {},
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

/** Shows the page visitors get while an app of the project can't answer, and turns maintenance mode on. */
export function MaintenanceCard({ project }: { project: Project }) {
  const qc = useQueryClient();
  const d = defaults(project.name);
  const [form, setForm] = useState(() => toForm(project.maintenance, d));
  const dirty = JSON.stringify(toInput(form, d)) !== JSON.stringify(toInput(toForm(project.maintenance, d), d));
  const formId = useId();
  const save = useMutation({
    meta: { error: "Couldn't save the maintenance page" },
    // The starting page is saved by its own card.
    mutationFn: () => api.setMaintenance(project.id, { ...toInput(form, d), starting: project.maintenance?.starting ?? {} }),
    onSuccess: (updated) => {
      qc.setQueryData(["project", project.id], updated);
      setForm(toForm(updated.maintenance, d));
      const was = !!project.maintenance?.enabled;
      toast.success(updated.maintenance.enabled === was ? "Maintenance page saved" : updated.maintenance.enabled ? "Maintenance mode on" : "Maintenance mode off", {
        description: "Traefik applies it within seconds; nothing restarted.",
      });
    },
  });
  // Custom HTML starts from the page visitors get now, the saved default one.
  const pick = (custom: boolean) => {
    setForm((f) => ({ ...f, custom }));
    if (!custom || form.html) return;
    fetch(`/api/pages/${project.id}`)
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
      title={<Applies title="Maintenance" when="now" />}
      description={
        <>
          Visitors of the project&apos;s apps get this page (503, so search engines come back later) while an app has no replica to answer:
          stopped, crashed or not ready yet.
        </>
      }
    >
      <form id={formId} onSubmit={onSubmit} className="space-y-6">
        <div className="space-y-3">
          <CheckboxField
            label="Maintenance mode"
            description="Every visitor of every app of the project gets the page while the replicas keep running, e.g. during a migration. Nothing restarts."
            checked={form.enabled}
            // The default message follows the mode; a custom one stays.
            onCheckedChange={(enabled) =>
              setForm({ ...form, enabled, message: form.message === (enabled ? d.off : d.on) ? (enabled ? d.on : d.off) : form.message })
            }
          />
          {form.enabled && (
            <FloatingTextarea
              label="Still reach the apps"
              value={form.allow}
              onChange={(e) => setForm({ ...form, allow: e.target.value })}
              placeholder={"203.0.113.7\n10.0.0.0/8"}
              className="[&_textarea]:font-mono"
              description="One IP or CIDR range per line, e.g. your own, to check the apps before reopening them. They still go through each app's access settings."
            />
          )}
        </div>

        <div className="space-y-3">
          <Segmented
            size="sm"
            value={form.custom ? "html" : "default"}
            onValueChange={(v) => pick(v === "html")}
            aria-label="Page"
          >
            <SegmentedItem value="default">
              <Type />
              Default page
            </SegmentedItem>
            <SegmentedItem value="html">
              <Code />
              Custom HTML
            </SegmentedItem>
          </Segmented>
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
            href={`/api/pages/${project.id}`}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1 text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
          >
            Open the saved page
            <ExternalLink className="size-3.5" />
          </a>
        </div>
      </form>
      <SaveBar form={formId} dirty={dirty} saving={save.isPending} onReset={() => setForm(toForm(project.maintenance, d))} label="Save maintenance" />
    </Section>
  );
}

const startingStarter = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Starting…</title>
</head>
<body>
  <h1>Starting…</h1>
  <p>This page reloads by itself in a few seconds.</p>
</body>
</html>`;

type StartingForm = { custom: boolean; title: string; message: string; html: string };

/** The default starting page's text, as the manager renders it (renderStarting). */
const startingDefaults = (name: string) => ({
  title: "Starting…",
  message: `${name} is starting. This page reloads by itself in a few seconds.`,
});

// The fields show the default text; left as is, it is saved empty.
const toStartingForm = (m: Maintenance | undefined, d: ReturnType<typeof startingDefaults>): StartingForm => ({
  custom: !!m?.starting?.html,
  title: m?.starting?.title || d.title,
  message: m?.starting?.message || d.message,
  html: m?.starting?.html ?? "",
});

/** The page visitors get while a sleeping project starts again. */
export function StartingCard({ project }: { project: Project }) {
  const qc = useQueryClient();
  const d = startingDefaults(project.name);
  const [form, setForm] = useState(() => toStartingForm(project.maintenance, d));
  const input = (f: StartingForm) => {
    if (f.custom) return { html: f.html };
    const title = f.title.trim(), message = f.message.trim();
    return { title: title === d.title ? "" : title, message: message === d.message ? "" : message };
  };
  const dirty = JSON.stringify(input(form)) !== JSON.stringify(input(toStartingForm(project.maintenance, d)));
  const formId = useId();
  const save = useMutation({
    meta: { error: "Couldn't save the starting page" },
    mutationFn: () => api.setMaintenance(project.id, { ...saved(project.maintenance), starting: input(form) }),
    onSuccess: (updated) => {
      qc.setQueryData(["project", project.id], updated);
      setForm(toStartingForm(updated.maintenance, d));
      toast.success("Starting page saved");
    },
  });
  // Custom HTML starts from the page visitors get now.
  const pick = (custom: boolean) => {
    setForm((f) => ({ ...f, custom }));
    if (!custom || form.html) return;
    fetch(`/api/pages/${project.id}?page=starting`)
      .then((r) => (r.headers.get("Content-Type")?.startsWith("text/html") ? r.text() : startingStarter))
      .catch(() => startingStarter)
      .then((html) => setForm((f) => (f.custom && !f.html ? { ...f, html } : f)));
  };
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };
  return (
    <Section
      title={<Applies title="Starting page" when="now" />}
      description={
        <>
          Visitors get this page while the project wakes from sleep. It reloads every 3 seconds until the app answers; API calls get a 503
          with <Mono>Retry-After</Mono> instead.
        </>
      }
    >
      <form id={formId} onSubmit={onSubmit} className="space-y-3">
        <Segmented size="sm" value={form.custom ? "html" : "default"} onValueChange={(v) => pick(v === "html")} aria-label="Page">
          <SegmentedItem value="default">
            <Type />
            Default page
          </SegmentedItem>
          <SegmentedItem value="html">
            <Code />
            Custom HTML
          </SegmentedItem>
        </Segmented>
        {form.custom ? (
          <div className="space-y-1.5">
            <Suspense fallback={<div className="h-64 rounded-md border" />}>
              <HtmlEditor label="Starting page HTML" value={form.html} onChange={(html) => setForm((f) => ({ ...f, html }))} />
            </Suspense>
            <p className="text-xs text-muted-foreground">
              A whole document, up to 64 KiB. The manager reloads it every 3 seconds (a <Mono>Refresh</Mono> header): no script needed.
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
            />
          </>
        )}
        <a
          href={`/api/pages/${project.id}?page=starting`}
          target="_blank"
          rel="noreferrer"
          className="inline-flex items-center gap-1 text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
        >
          Open the saved page
          <ExternalLink className="size-3.5" />
        </a>
      </form>
      <SaveBar
        form={formId}
        dirty={dirty}
        saving={save.isPending}
        onReset={() => setForm(toStartingForm(project.maintenance, d))}
        label="Save starting page"
      />
    </Section>
  );
}
