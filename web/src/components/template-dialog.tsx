import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, ExternalLink, LayoutGrid, Rocket, ScanSearch, Search } from "lucide-react";
import { useState, type FormEvent, type ReactNode } from "react";
import { useNavigate } from "react-router";
import { toast } from "sonner";
import { PlanView } from "@/components/compose-dialog";
import { CheckboxField, EmptyState, ErrorText, Loading } from "@/components/common";
import { ServiceIconTile } from "@/components/service-icon";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingSelect } from "@/components/ui/floating-select";
import { useDocsUrl } from "@/lib/docs";
import { api, type AppTemplate, type TemplateInstall, type TemplateResult } from "../api";

/**
 * Installs a one-click app: into projectId when given, else into a new
 * project (then opened).
 */
export function TemplateDialog({ projectId }: { projectId?: string }) {
  const [open, setOpen] = useState(false);
  const [picked, setPicked] = useState<AppTemplate | null>(null);
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (!o) setPicked(null);
      }}
    >
      <DialogTrigger asChild>
        <Button size="sm" variant="outline">
          <LayoutGrid data-icon="inline-start" />
          {projectId ? "Add an app" : "From a template"}
        </Button>
      </DialogTrigger>
      <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-2xl">
        {picked ? (
          <InstallForm key={picked.id} template={picked} projectId={projectId} onBack={() => setPicked(null)} onDone={() => setOpen(false)} />
        ) : (
          <>
            <DialogHeader>
              <DialogTitle>App templates</DialogTitle>
              <DialogDescription>
                Ready-made services for common self-hosted apps. You see what will be created before anything is.
              </DialogDescription>
            </DialogHeader>
            {open && <Gallery onPick={setPicked} />}
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

function Gallery({ onPick }: { onPick: (t: AppTemplate) => void }) {
  const templates = useQuery({ queryKey: ["templates"], queryFn: api.templates, staleTime: Infinity });
  const [search, setSearch] = useState("");
  const [category, setCategory] = useState("");
  if (templates.isPending) return <Loading />;
  if (templates.error) return <ErrorText error={templates.error} />;
  if (templates.data.length === 0) return <EmptyState icon={LayoutGrid} title="No templates" description="This version ships none." />;

  const categories = [...new Set(templates.data.map((t) => t.category))].sort();
  const words = search.toLowerCase().split(/\s+/).filter(Boolean);
  const shown = templates.data.filter((t) => {
    const text = [t.title, t.description, t.category, ...t.tags].join(" ").toLowerCase();
    return (!category || t.category === category) && words.every((w) => text.includes(w));
  });
  return (
    <div className="space-y-3">
      <FloatingInput
        label="Search"
        size="sm"
        icon={<Search className="size-4" />}
        value={search}
        onChange={(e) => setSearch(e.target.value)}
        autoFocus
      />
      {categories.length > 1 && (
        <div className="flex flex-wrap gap-1.5">
          {["", ...categories].map((c) => (
            <Button key={c} type="button" size="xs" variant={category === c ? "secondary" : "ghost"} onClick={() => setCategory(c)} className="capitalize">
              {c || "All"}
            </Button>
          ))}
        </div>
      )}
      {shown.length === 0 ? (
        <p className="py-6 text-center text-sm text-muted-foreground">No template matches.</p>
      ) : (
        <div className="grid gap-3 sm:grid-cols-2">
          {shown.map((t) => (
            <button
              key={t.id}
              type="button"
              onClick={() => onPick(t)}
              className="flex items-start gap-3 rounded-lg border p-3 text-left transition-colors hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
            >
              <ServiceIconTile service={{ icon: t.icon }} />
              <span className="min-w-0 space-y-0.5">
                <span className="block font-medium">{t.title}</span>
                <span className="block text-xs text-muted-foreground">{t.description}</span>
              </span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function InstallForm({
  template: t,
  projectId,
  onBack,
  onDone,
}: {
  template: AppTemplate;
  projectId?: string;
  onBack: () => void;
  onDone: () => void;
}) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const servers = useQuery({ queryKey: ["servers"], queryFn: api.servers, enabled: !projectId });
  const asked = t.inputs.filter((i) => !i.generate);
  const [values, setValues] = useState<Record<string, string>>(() => Object.fromEntries(asked.map((i) => [i.name, i.default ?? ""])));
  const [name, setName] = useState(t.project);
  const [serverId, setServerId] = useState("local");
  const [plan, setPlan] = useState<TemplateResult["plan"] | null>(null);
  const changed = () => setPlan(null);

  const body = (dryRun: boolean): TemplateInstall => ({
    ...(projectId ? { projectId } : { newProject: { name: name.trim(), serverId } }),
    // Unset optional inputs take the template's default.
    values: Object.fromEntries(Object.entries(values).filter(([, v]) => v.trim())),
    dryRun,
  });
  const preview = useMutation({
    mutationFn: () => api.installTemplate(t.id, body(true)),
    onSuccess: (res) => setPlan(res.plan),
  });
  const install = useMutation({
    mutationFn: () => api.installTemplate(t.id, body(false)),
    onSuccess: (res) => {
      qc.invalidateQueries({ queryKey: ["projects"] });
      qc.invalidateQueries({ queryKey: ["topology"] });
      if (res.projectId) {
        qc.invalidateQueries({ queryKey: ["services", res.projectId] });
        qc.invalidateQueries({ queryKey: ["project", res.projectId] });
      }
      toast.success(`${t.title} installed`, {
        description: res.plan.deploying.length ? `Deploying ${res.plan.deploying.join(", ")}` : undefined,
      });
      onDone();
      if (!projectId && res.projectId) navigate(`/projects/${res.projectId}`);
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (plan) install.mutate();
    else preview.mutate();
  };
  const generated = t.inputs.filter((i) => i.generate);

  return (
    <form onSubmit={onSubmit} className="contents">
      <DialogHeader>
        <DialogTitle className="flex items-center gap-3">
          <ServiceIconTile service={{ icon: t.icon }} className="size-8" />
          {t.title}
        </DialogTitle>
        <DialogDescription>
          {t.description}{" "}
          {(t.docs || t.website) && (
            <a href={t.docs || t.website} target="_blank" rel="noreferrer" className="inline-flex items-center gap-0.5 underline underline-offset-2">
              Documentation
              <ExternalLink className="size-3" />
            </a>
          )}
        </DialogDescription>
      </DialogHeader>
      <div className="space-y-4">
        {t.notes && <Notes markdown={t.notes} />}
        {!projectId && (
          <div className="grid gap-3 sm:grid-cols-2">
            <FloatingInput
              label="Project name"
              required
              value={name}
              onChange={(e) => {
                setName(e.target.value);
                changed();
              }}
              className={(servers.data?.length ?? 0) > 1 ? undefined : "sm:col-span-2"}
            />
            {(servers.data?.length ?? 0) > 1 && (
              <FloatingSelect
                label="Server"
                value={serverId}
                onValueChange={(v) => {
                  setServerId(v);
                  changed();
                }}
                options={servers.data!.map((s) => ({ value: s.id, label: s.name }))}
              />
            )}
          </div>
        )}
        {asked.map((i) =>
          i.type === "checkbox" ? (
            <CheckboxField
              key={i.name}
              label={i.label}
              description={i.help && <Linked text={i.help} />}
              checked={values[i.name] === "true"}
              onCheckedChange={(v) => {
                setValues((vs) => ({ ...vs, [i.name]: v ? "true" : "" }));
                changed();
              }}
            />
          ) : i.type === "select" ? (
            <FloatingSelect
              key={i.name}
              label={i.label}
              description={i.help && <Linked text={i.help} />}
              value={values[i.name] ?? ""}
              onValueChange={(v) => {
                setValues((vs) => ({ ...vs, [i.name]: v }));
                changed();
              }}
              options={(i.options ?? []).map((o) => ({ value: o, label: o }))}
            />
          ) : (
            <FloatingInput
              key={i.name}
              label={i.required || i.default ? i.label : `${i.label} (optional)`}
              required={i.required}
              type={i.type === "secret" ? "password" : i.type === "url" ? "url" : "text"}
              autoComplete="off"
              placeholder={i.placeholder}
              description={i.help && <Linked text={i.help} />}
              value={values[i.name] ?? ""}
              onChange={(e) => {
                setValues((v) => ({ ...v, [i.name]: e.target.value }));
                changed();
              }}
            />
          ),
        )}
        {generated.length > 0 && (
          <p className="text-xs text-muted-foreground">
            Generated for you and kept as secrets: {generated.map((i) => i.label || i.name).join(", ")}.
          </p>
        )}
        {plan && <PlanView plan={plan} />}
        <ErrorText error={preview.error ?? install.error} />
      </div>
      <DialogFooter className="sm:justify-between">
        <Button type="button" variant="ghost" onClick={onBack}>
          <ArrowLeft data-icon="inline-start" />
          Templates
        </Button>
        {plan ? (
          <Button type="submit" disabled={install.isPending}>
            <Rocket data-icon="inline-start" />
            Install and deploy
          </Button>
        ) : (
          <Button type="submit" disabled={preview.isPending}>
            <ScanSearch data-icon="inline-start" />
            Preview
          </Button>
        )}
      </DialogFooter>
    </form>
  );
}

/** Help text with its URLs as links. */
function Linked({ text }: { text: string }) {
  return text.split(/(https:\/\/[^\s,]+[^\s,.])/).map((part, i) =>
    i % 2 ? (
      <a key={i} href={part} target="_blank" rel="noreferrer" className="underline underline-offset-2">
        {part.replace(/^https:\/\//, "")}
      </a>
    ) : (
      part
    ),
  );
}

/**
 * A template's notes: the markdown subset they're written in (paragraphs,
 * lists, **bold**, `code`, [links](…)). A /docs/<page> link opens that page
 * of the running version's docs.
 */
function Notes({ markdown }: { markdown: string }) {
  const docs = useDocsUrl();
  const inline = (text: string) =>
    text.split(/(\*\*[^*]+\*\*|`[^`]+`|\[[^\]]+\]\([^)\s]+\))/).map((part, i): ReactNode => {
      if (i % 2 === 0) return part;
      if (part.startsWith("**")) return <strong key={i}>{part.slice(2, -2)}</strong>;
      if (part.startsWith("`")) return <code key={i} className="rounded bg-muted px-1 font-mono text-[0.9em]">{part.slice(1, -1)}</code>;
      const [, label, href] = part.match(/^\[(.+)\]\((.+)\)$/)!;
      return (
        <a key={i} href={href.replace(/^\/docs(?=\/|#|$)/, docs)} target="_blank" rel="noreferrer" className="underline underline-offset-2">
          {label}
        </a>
      );
    });
  return (
    <div className="space-y-2 rounded-lg border bg-muted/30 p-3 text-sm text-muted-foreground">
      {markdown.split(/\n\s*\n/).map((block, i) => {
        const lines = block.trim().split("\n");
        const items = (re: RegExp) => lines.every((l) => re.test(l)) && lines.map((l, j) => <li key={j}>{inline(l.replace(re, ""))}</li>);
        const bullets = items(/^[-*] /);
        if (bullets) return <ul key={i} className="list-disc space-y-1 pl-5">{bullets}</ul>;
        const numbered = items(/^\d+\. /);
        if (numbered) return <ol key={i} className="list-decimal space-y-1 pl-5">{numbered}</ol>;
        return <p key={i}>{inline(lines.join(" "))}</p>;
      })}
    </div>
  );
}
