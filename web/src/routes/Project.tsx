import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Database, DatabaseBackup, Plus, Trash2 } from "lucide-react";
import { useState, type FormEvent } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { toast } from "sonner";
import { Empty, ErrorText, StateBadge } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DomainField } from "@/components/domain-field";
import { PageBody, PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingSelect } from "@/components/ui/floating-select";
import { FloatingTextarea } from "@/components/ui/floating-textarea";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { parseEnv, serviceState } from "@/lib/format";
import { api, type ServiceInput } from "../api";

export function Project() {
  const { id = "" } = useParams();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const project = useQuery({ queryKey: ["project", id], queryFn: () => api.project(id) });
  const services = useQuery({
    queryKey: ["services", id],
    queryFn: () => api.services(id),
    refetchInterval: 5_000,
  });

  const backupAll = useMutation({
    mutationFn: () => api.backupProject(id),
    onSuccess: (res) => {
      qc.invalidateQueries({ queryKey: ["backups"] });
      if (res.error) toast.warning("Some databases were not backed up", { description: res.error });
      else toast.success("Backups started");
    },
  });
  const hasDatabases = services.data?.some((s) => s.kind === "postgres");
  const remove = useMutation({
    mutationFn: (confirm: string) => api.deleteProject(id, confirm),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["projects"] });
      navigate("/");
    },
  });

  const crumbs = [{ label: "Projects", to: "/" }, { label: project.data?.name ?? "…" }];
  if (project.error)
    return (
      <>
        <PageHeader crumbs={crumbs} />
        <PageBody>
          <ErrorText error={project.error} />
        </PageBody>
      </>
    );

  return (
    <>
      <PageHeader
        crumbs={crumbs}
        actions={
          project.data && (
            <>
              {hasDatabases && (
                <Button variant="outline" size="sm" disabled={backupAll.isPending} onClick={() => backupAll.mutate()}>
                  <DatabaseBackup data-icon="inline-start" />
                  Back up databases
                </Button>
              )}
              <NewServiceDialog projectId={id} />
              <ConfirmDialog
                trigger={
                  <Button variant="destructive" size="icon-sm" aria-label="Delete project" disabled={remove.isPending}>
                    <Trash2 />
                  </Button>
                }
                title={`Delete project ${project.data.name}?`}
                description="All its containers and database data will be destroyed."
                typeToConfirm={project.data.name}
                onConfirm={(name) => remove.mutate(name)}
              />
            </>
          )
        }
      />
      <PageBody>
        <ErrorText error={remove.error ?? backupAll.error} />
        {services.error ? (
          <ErrorText error={services.error} />
        ) : !services.data ? (
          <Empty>Loading…</Empty>
        ) : services.data.length === 0 ? (
          <Card className="items-center gap-2 py-10 text-center">
            <p className="font-medium">No services yet</p>
            <p className="text-sm text-muted-foreground">Add an app from an image or a Git repository, or a PostgreSQL database.</p>
          </Card>
        ) : (
          <div className="grid gap-3 sm:grid-cols-2">
            {services.data.map((s) => (
              <Link
                key={s.id}
                to={`/services/${s.id}`}
                className="group rounded-xl outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
              >
                <Card size="sm" className="h-full transition-colors group-hover:bg-muted/50">
                  <CardHeader className="flex items-center justify-between gap-2">
                    <CardTitle className="flex min-w-0 items-center gap-2">
                      {s.kind === "postgres" && <Database className="size-4 shrink-0 text-muted-foreground" />}
                      <span className="truncate">{s.name}</span>
                    </CardTitle>
                    <StateBadge state={serviceState(s.containers)} />
                  </CardHeader>
                  <CardContent className="space-y-1">
                    <p className="truncate font-mono text-xs text-muted-foreground">
                      {s.source === "git" ? `${s.gitUrl.replace(/^https?:\/\//, "")}@${s.gitBranch}` : s.image}
                    </p>
                    <p className="text-xs text-muted-foreground">
                      {s.kind === "postgres"
                        ? `PostgreSQL · ${s.memoryMb} MB`
                        : `${s.domain || "private"} · ${s.replicas} replica${s.replicas > 1 ? "s" : ""}`}
                    </p>
                  </CardContent>
                </Card>
              </Link>
            ))}
          </div>
        )}
      </PageBody>
    </>
  );
}

const emptyForm = {
  name: "",
  image: "",
  port: "",
  domain: "",
  replicas: "1",
  env: "",
  version: "17",
  memory: "512",
  databaseId: "",
  gitUrl: "",
  gitBranch: "main",
  gitToken: "",
  dockerfile: "Dockerfile",
  buildContext: "",
};

function NewServiceDialog({ projectId }: { projectId: string }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const services = useQuery({ queryKey: ["services", projectId], queryFn: () => api.services(projectId) });
  const databases = services.data?.filter((s) => s.kind === "postgres") ?? [];

  const [open, setOpen] = useState(false);
  const [kind, setKind] = useState<"app" | "postgres">("app");
  const [source, setSource] = useState<"image" | "git">("image");
  const [form, setForm] = useState(emptyForm);
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });
  // Default an app to the project's only database.
  const databaseId = form.databaseId || (databases.length === 1 ? databases[0].id : "none");

  const create = useMutation({
    mutationFn: async () => {
      const input: ServiceInput =
        kind === "postgres"
          ? {
              kind,
              name: form.name.trim(),
              image: `postgres:${form.version}-alpine`,
              memoryMb: Number(form.memory) || 512,
            }
          : {
              kind,
              name: form.name.trim(),
              ...(source === "git"
                ? {
                    source,
                    gitUrl: form.gitUrl.trim(),
                    gitBranch: form.gitBranch.trim(),
                    gitToken: form.gitToken.trim(),
                    dockerfile: form.dockerfile.trim(),
                    buildContext: form.buildContext.trim(),
                  }
                : { image: form.image.trim() }),
              port: Number(form.port) || 0,
              domain: form.domain.trim(),
              replicas: Number(form.replicas) || 1,
              env: parseEnv(form.env),
              databaseId: databaseId === "none" ? "" : databaseId,
            };
      const svc = await api.createService(projectId, input);
      await api.deploy(svc.id);
      return svc;
    },
    onSuccess: (svc) => {
      qc.invalidateQueries({ queryKey: ["services", projectId] });
      setOpen(false);
      navigate(`/services/${svc.id}`);
    },
  });

  const onOpenChange = (next: boolean) => {
    if (create.isPending) return;
    setOpen(next);
    if (!next) {
      setForm(emptyForm);
      setKind("app");
      setSource("image");
      create.reset();
    }
  };
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    create.mutate();
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button size="sm">
          <Plus data-icon="inline-start" />
          New service
        </Button>
      </DialogTrigger>
      <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-2xl">
        <form onSubmit={onSubmit} className="contents">
          <DialogHeader>
            <DialogTitle>New service</DialogTitle>
            <DialogDescription>It is deployed as soon as it is created.</DialogDescription>
          </DialogHeader>
          <ToggleGroup
            type="single"
            variant="outline"
            spacing={0}
            value={kind}
            onValueChange={(v) => v && setKind(v as "app" | "postgres")}
            className="w-full"
          >
            <ToggleGroupItem value="app" className="flex-1 aria-checked:bg-muted">
              App
            </ToggleGroupItem>
            <ToggleGroupItem value="postgres" className="flex-1 aria-checked:bg-muted">
              PostgreSQL
            </ToggleGroupItem>
          </ToggleGroup>
          <div className="grid gap-4 sm:grid-cols-2">
            <FloatingInput
              label="Name"
              required
              autoFocus
              value={form.name}
              onChange={set("name")}
              placeholder={kind === "postgres" ? "db" : "web"}
              description={kind === "postgres" ? "Also the hostname apps use to connect." : undefined}
            />
            {kind === "postgres" ? (
              <>
                <FloatingSelect
                  label="Version"
                  value={form.version}
                  onValueChange={(v) => setForm({ ...form, version: v })}
                  options={["18", "17", "16", "15"].map((v) => ({ value: v, label: `PostgreSQL ${v}` }))}
                />
                <FloatingInput
                  label="Memory (MB)"
                  type="number"
                  min={128}
                  step={128}
                  value={form.memory}
                  onChange={set("memory")}
                  description="Container limit; shared_buffers is sized from it."
                />
              </>
            ) : (
              <>
                <FloatingSelect
                  label="Source"
                  value={source}
                  onValueChange={(v) => setSource(v as "image" | "git")}
                  options={[
                    { value: "image", label: "Docker image" },
                    { value: "git", label: "Git repository (Dockerfile)" },
                  ]}
                />
                {source === "image" ? (
                  <FloatingInput
                    label="Image"
                    required
                    value={form.image}
                    onChange={set("image")}
                    placeholder="ghcr.io/org/app:latest"
                    className="sm:col-span-2"
                  />
                ) : (
                  <GitFields form={form} set={set} />
                )}
                <DomainField value={form.domain} onChange={(domain) => setForm({ ...form, domain })} className="sm:col-span-2" />
                <FloatingInput
                  label="Container port"
                  type="number"
                  min={1}
                  max={65535}
                  value={form.port}
                  onChange={set("port")}
                  placeholder="3000"
                  description="The port your app listens on. Required with a domain."
                />
                <FloatingInput label="Replicas" type="number" min={1} max={10} value={form.replicas} onChange={set("replicas")} />
                <FloatingSelect
                  label="Database"
                  value={databaseId}
                  onValueChange={(v) => setForm({ ...form, databaseId: v })}
                  options={[{ value: "none", label: "None" }, ...databases.map((d) => ({ value: d.id, label: d.name }))]}
                  description="Injects DATABASE_URL."
                />
                <FloatingTextarea
                  label="Environment"
                  rows={4}
                  value={form.env}
                  onChange={set("env")}
                  placeholder="NODE_ENV=production"
                  className="sm:col-span-2 [&_textarea]:font-mono [&_textarea]:text-sm"
                  description="One KEY=value per line. Values are hidden once saved."
                />
              </>
            )}
          </div>
          <ErrorText error={create.error} />
          <DialogFooter>
            <Button type="submit" disabled={create.isPending}>
              {create.isPending ? "Creating…" : "Create & deploy"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

type GitForm = { gitUrl: string; gitBranch: string; gitToken: string; dockerfile: string; buildContext: string };

export function GitFields({
  form,
  set,
  tokenHint,
}: {
  form: GitForm;
  set: (k: keyof GitForm) => (e: { target: { value: string } }) => void;
  tokenHint?: string;
}) {
  return (
    <>
      <FloatingInput
        label="Repository"
        required
        value={form.gitUrl}
        onChange={set("gitUrl")}
        placeholder="https://github.com/org/app"
        description="HTTPS URL; built with its Dockerfile on every deploy."
        className="sm:col-span-2"
      />
      <FloatingInput label="Branch" required value={form.gitBranch} onChange={set("gitBranch")} />
      <FloatingInput
        label="Access token"
        type="password"
        autoComplete="off"
        value={form.gitToken}
        onChange={set("gitToken")}
        description={tokenHint ?? "Only for private repositories."}
      />
      <FloatingInput label="Dockerfile" value={form.dockerfile} onChange={set("dockerfile")} inputClassName="font-mono" />
      <FloatingInput
        label="Build context"
        value={form.buildContext}
        onChange={set("buildContext")}
        placeholder="."
        inputClassName="font-mono"
        description="Folder inside the repository; empty for the root."
      />
    </>
  );
}
