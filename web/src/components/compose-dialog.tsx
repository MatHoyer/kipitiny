import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, FileCode, FileUp, KeyRound, Rocket, ScanSearch } from "lucide-react";
import { useState, type ChangeEvent, type FormEvent } from "react";
import { toast } from "sonner";
import { CheckboxField, CopyButton, ErrorText, Loading, Tag } from "@/components/common";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingTextarea } from "@/components/ui/floating-textarea";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { DOCS_URL } from "@/lib/docs";
import { api, type ComposePlan } from "../api";

/** Shows a project (or one service) as a docker-compose file, and exports it
 * with its secrets to move it to another kipitiny. */
export function ComposeDialog({ projectId, service, canImport = false }: { projectId: string; service?: string; canImport?: boolean }) {
  const [open, setOpen] = useState(false);
  const [tab, setTab] = useState("view");
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm" variant="outline">
          <FileCode data-icon="inline-start" />
          Compose
        </Button>
      </DialogTrigger>
      <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>{service ? `${service} as compose` : "Project as compose"}</DialogTitle>
          <DialogDescription>
            Standard docker-compose; kipitiny settings live in <code>x-kipitiny</code> blocks, which Docker Compose ignores. See the{" "}
            <a href={`${DOCS_URL}/compose`} target="_blank" rel="noreferrer" className="underline underline-offset-2">
              compose reference
            </a>
            .
          </DialogDescription>
        </DialogHeader>
        <Tabs value={tab} onValueChange={setTab}>
          <TabsList variant="line">
            <TabsTrigger value="view">View</TabsTrigger>
            <TabsTrigger value="export">Export with secrets</TabsTrigger>
            {canImport && <TabsTrigger value="import">Import</TabsTrigger>}
          </TabsList>
          <TabsContent value="view">{open && <ComposeView projectId={projectId} service={service} />}</TabsContent>
          <TabsContent value="export">
            <ExportBundle projectId={projectId} service={service} onDone={() => setOpen(false)} />
          </TabsContent>
          {canImport && (
            <TabsContent value="import">
              <ImportCompose projectId={projectId} onDone={() => setOpen(false)} />
            </TabsContent>
          )}
        </Tabs>
      </DialogContent>
    </Dialog>
  );
}

function ComposeView({ projectId, service }: { projectId: string; service?: string }) {
  const compose = useQuery({ queryKey: ["compose", projectId, service], queryFn: () => api.compose(projectId, service) });
  if (compose.error) return <ErrorText error={compose.error} />;
  if (compose.data === undefined) return <Loading />;
  return (
    <div className="space-y-3">
      <div className="relative">
        <pre className="max-h-[55svh] overflow-auto rounded-md border bg-muted/40 p-3 font-mono text-xs leading-relaxed">{compose.data}</pre>
        <div className="absolute top-2 right-2">
          <CopyButton value={compose.data} />
        </div>
      </div>
      <p className="text-xs text-muted-foreground">
        Secrets are <code>{"${NAME}"}</code> variables without values. Data (databases, volumes) moves with a backup restored on the new
        server.
      </p>
      <Button size="sm" variant="outline" asChild>
        <a href={api.composeUrl(projectId, service)} download>
          <Download data-icon="inline-start" />
          Download compose.yaml
        </a>
      </Button>
    </div>
  );
}

function ExportBundle({ projectId, service, onDone }: { projectId: string; service?: string; onDone: () => void }) {
  const [password, setPassword] = useState("");
  const exportBundle = useMutation({
    mutationFn: () => api.exportBundle(projectId, password, service),
    onSuccess: (blob) => {
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = "kipitiny-export.zip";
      a.click();
      URL.revokeObjectURL(url);
      setPassword("");
      onDone();
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    exportBundle.mutate();
  };
  return (
    <form onSubmit={onSubmit} className="space-y-4">
      <p className="text-sm text-muted-foreground">
        Downloads <code>compose.yaml</code> and a <code>.env</code> holding every secret value (env secrets, project secrets, database
        passwords, basic auth hashes). Import both on another kipitiny, or run them with{" "}
        <code>docker compose --env-file .env up</code>. Keep the file safe.
      </p>
      <FloatingInput
        label="Your password"
        type="password"
        autoComplete="current-password"
        required
        value={password}
        onChange={(e) => setPassword(e.target.value)}
      />
      <ErrorText error={exportBundle.error} />
      <DialogFooter>
        <Button type="submit" disabled={!password || exportBundle.isPending}>
          <KeyRound data-icon="inline-start" />
          Export with secrets
        </Button>
      </DialogFooter>
    </form>
  );
}

/** Applies a compose file (and its .env) to the project, after a preview. */
function ImportCompose({ projectId, onDone }: { projectId: string; onDone: () => void }) {
  const qc = useQueryClient();
  const [compose, setCompose] = useState("");
  const [env, setEnv] = useState("");
  const [prune, setPrune] = useState(false);
  const [plan, setPlan] = useState<ComposePlan | null>(null);
  const edit = (f: (v: string) => void) => (v: string) => {
    f(v);
    setPlan(null);
  };
  const preview = useMutation({
    mutationFn: () => api.applyCompose(projectId, { compose, env, prune, dryRun: true }),
    onSuccess: setPlan,
  });
  const apply = useMutation({
    mutationFn: () => api.applyCompose(projectId, { compose, env, prune, deploy: true }),
    onSuccess: (res) => {
      qc.invalidateQueries({ queryKey: ["services", projectId] });
      qc.invalidateQueries({ queryKey: ["project", projectId] });
      toast.success("Compose applied", {
        description: res.deploying.length ? `Deploying ${res.deploying.join(", ")}` : undefined,
      });
      onDone();
    },
  });
  const load = (f: (v: string) => void) => async (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (file) edit(f)(await file.text());
    e.target.value = "";
  };
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (plan) apply.mutate();
    else preview.mutate();
  };
  const empty = plan && plan.create.length + plan.update.length + plan.delete.length + plan.variables.length === 0;
  return (
    <form onSubmit={onSubmit} className="space-y-4">
      <p className="text-sm text-muted-foreground">
        Creates the services the file lists and updates the ones that differ. <code>{"${NAME}"}</code> variables take their value from
        the .env, else reference the project variable <code>NAME</code>; secrets exported without a value keep the current one.
      </p>
      <FloatingTextarea
        label="compose.yaml"
        required
        value={compose}
        onChange={(e) => edit(setCompose)(e.target.value)}
        className="font-mono [&_textarea]:max-h-[40svh] [&_textarea]:text-xs"
      />
      <FloatingTextarea
        label=".env (optional)"
        value={env}
        onChange={(e) => edit(setEnv)(e.target.value)}
        className="font-mono [&_textarea]:max-h-[20svh] [&_textarea]:text-xs"
      />
      <div className="flex flex-wrap gap-2">
        <FileButton label="Load compose file" accept=".yaml,.yml" onChange={load(setCompose)} />
        <FileButton label="Load .env" accept=".env,text/plain" onChange={load(setEnv)} />
      </div>
      <CheckboxField
        label="Delete apps the file doesn't list"
        description="With their volumes. Databases are never deleted, only reported."
        checked={prune}
        onCheckedChange={(v) => {
          setPrune(v);
          setPlan(null);
        }}
      />
      {plan && <PlanView plan={plan} />}
      <ErrorText error={preview.error ?? apply.error} />
      <DialogFooter>
        {plan ? (
          <Button type="submit" disabled={apply.isPending || !!empty}>
            <Rocket data-icon="inline-start" />
            {empty ? "Nothing to change" : "Apply and deploy"}
          </Button>
        ) : (
          <Button type="submit" disabled={!compose.trim() || preview.isPending}>
            <ScanSearch data-icon="inline-start" />
            Preview changes
          </Button>
        )}
      </DialogFooter>
    </form>
  );
}

function FileButton({ label, accept, onChange }: { label: string; accept: string; onChange: (e: ChangeEvent<HTMLInputElement>) => void }) {
  return (
    <Button type="button" size="sm" variant="outline" asChild>
      <label className="cursor-pointer">
        <FileUp data-icon="inline-start" />
        {label}
        <input type="file" accept={accept} className="sr-only" onChange={onChange} />
      </label>
    </Button>
  );
}

/** What applying a compose file changes. */
export function PlanView({ plan }: { plan: ComposePlan }) {
  const rows: [string, string[], string?][] = [
    ["Create", plan.create],
    ["Update", plan.update.map((u) => `${u.name} (${u.fields.join(", ")})`)],
    ["Delete", plan.delete, "text-destructive"],
    ["Kept, no longer listed", plan.orphaned],
    ["Project variables", plan.variables],
    ["Unchanged", plan.unchanged],
  ];
  return (
    <div className="space-y-2 rounded-md border p-3 text-sm">
      {rows
        .filter(([, items]) => items.length > 0)
        .map(([label, items, cls]) => (
          <div key={label} className="flex flex-wrap items-baseline gap-1.5">
            <span className="w-44 shrink-0 text-muted-foreground">{label}</span>
            {items.map((i) => (
              <Tag key={i} className={cls}>
                {i}
              </Tag>
            ))}
          </div>
        ))}
      {plan.warnings.length > 0 && (
        <ul className="list-disc space-y-0.5 pl-5 text-xs text-amber-600 dark:text-amber-400">
          {plan.warnings.map((w) => (
            <li key={w}>{w}</li>
          ))}
        </ul>
      )}
    </div>
  );
}
