import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Box, Database, DatabaseBackup, Plus, Trash2 } from "lucide-react";
import { useState, type FormEvent, type ReactNode } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { toast } from "sonner";
import { PostgresIcon } from "@/components/brand-icons";
import { CopyButton, Empty, ErrorText, Mono, Section, StateBadge } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DomainField } from "@/components/domain-field";
import { EnvEditor } from "@/components/env-editor";
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { useTab } from "@/hooks/use-tab";
import { cn } from "@/lib/utils";
import { dbFields, dbRef, envMap, envRows, envSecrets, serviceState, type EnvRow } from "@/lib/format";
import { api, type Project as ProjectT, type ServiceInput } from "../api";

export function Project() {
  const { id = "" } = useParams();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const project = useQuery({ queryKey: ["project", id], queryFn: () => api.project(id) });
  const [tab, setTab] = useTab(["services", "environment"], "services");
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
        <Tabs value={tab} onValueChange={setTab}>
          <TabsList variant="line">
            <TabsTrigger value="services">Services</TabsTrigger>
            <TabsTrigger value="environment">Environment</TabsTrigger>
          </TabsList>
          <TabsContent value="services">
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
          </TabsContent>
          <TabsContent value="environment" className="space-y-4">
            {project.data && <SharedVariables key={project.data.id} project={project.data} />}
            <DatabaseReferences names={services.data?.filter((s) => s.kind === "postgres").map((s) => s.name) ?? []} />
          </TabsContent>
        </Tabs>
      </PageBody>
    </>
  );
}

function SharedVariables({ project }: { project: ProjectT }) {
  const qc = useQueryClient();
  const [rows, setRows] = useState<EnvRow[]>(() => envRows(project.env, project.secrets));
  const save = useMutation({
    mutationFn: () => api.setProjectEnv(project.id, envMap(rows), envSecrets(rows)),
    onSuccess: (updated) => {
      qc.setQueryData(["project", project.id], updated);
      setRows(envRows(updated.env, updated.secrets));
      toast.success("Shared variables saved", { description: "Deploy the services that use them to apply." });
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <Section
      title="Shared variables & secrets"
      description={
        <>
          Available to every service of the project as <Mono>{"{{ project.NAME }}"}</Mono>.
        </>
      }
    >
      <form onSubmit={onSubmit} className="space-y-4">
        <EnvEditor
          showLabel={false}
          rows={rows}
          onChange={setRows}
          description="An entry used by a service can't be removed."
        />
        <div className="flex items-center justify-end gap-3">
          <ErrorText error={save.error} />
          <Button type="submit" disabled={save.isPending}>
            {save.isPending ? "Saving…" : "Save"}
          </Button>
        </div>
      </form>
    </Section>
  );
}

/** The credentials each database offers to the services' env. */
function DatabaseReferences({ names }: { names: string[] }) {
  if (names.length === 0) return null;
  return (
    <Section
      title="Databases"
      description="Credentials a service can use in its environment, e.g. under Secrets → Connect database."
    >
      {names.map((db) => (
        <div key={db} className="space-y-1.5">
          <p className="flex items-center gap-2 text-sm font-medium">
            <PostgresIcon className="size-4 text-[#4169E1]" />
            {db}
          </p>
          <ul className="flex flex-wrap gap-1.5">
            {dbFields.map((f) => (
              <li key={f} className="inline-flex items-center gap-0.5 rounded-md bg-muted py-0.5 pr-0.5 pl-2 font-mono text-xs">
                {dbRef(db, f)}
                <CopyButton value={dbRef(db, f)} label={`Copy ${f} reference`} />
              </li>
            ))}
          </ul>
        </div>
      ))}
    </Section>
  );
}

const emptyForm = {
  name: "",
  image: "",
  port: "",
  domain: "",
  replicas: "1",
  version: "17",
  memory: "512",
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
  const project = useQuery({ queryKey: ["project", projectId], queryFn: () => api.project(projectId) });

  const [open, setOpen] = useState(false);
  const [env, setEnv] = useState<EnvRow[]>([]);
  const [tab, setTab] = useState<"app" | "databases">("app");
  const [engine, setEngine] = useState<"postgres">("postgres");
  const kind = tab === "app" ? "app" : engine;
  const [source, setSource] = useState<"image" | "git">("image");
  const [form, setForm] = useState(emptyForm);
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });

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
              env: envMap(env),
              secrets: envSecrets(env),
            };
      const svc = await api.createService(projectId, input);
      await api.deploy(svc.id);
      return svc;
    },
    onSuccess: (svc) => {
      qc.invalidateQueries({ queryKey: ["services", projectId] });
      close();
      navigate(`/services/${svc.id}`);
    },
  });

  // Every new service starts as an app from a Docker image.
  const close = () => {
    setOpen(false);
    setForm(emptyForm);
    setEnv([]);
    setTab("app");
    setEngine("postgres");
    setSource("image");
    create.reset();
  };
  const onOpenChange = (next: boolean) => {
    if (create.isPending) return;
    if (!next) return close();
    // With a single database, apps most likely want it.
    if (databases.length === 1) setEnv([{ key: "DATABASE_URL", value: dbRef(databases[0].name, "URL"), secret: true }]);
    setOpen(true);
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
            value={tab}
            onValueChange={(v) => v && setTab(v as "app" | "databases")}
            className="w-full"
          >
            <ToggleGroupItem value="app" className="flex-1 aria-checked:bg-muted">
              <Box />
              App
            </ToggleGroupItem>
            <ToggleGroupItem value="databases" className="flex-1 aria-checked:bg-muted">
              <Database />
              Databases
            </ToggleGroupItem>
          </ToggleGroup>
          {tab === "databases" && (
            <div role="radiogroup" aria-label="Database engine" className="grid gap-3 sm:grid-cols-3">
              <EngineCard
                selected={engine === "postgres"}
                onSelect={() => setEngine("postgres")}
                icon={<PostgresIcon className="size-7 text-[#4169E1]" />}
                title="PostgreSQL"
                description="Relational database"
              />
            </div>
          )}
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
                  // Its own instance: Radix keeps the Source select's item text otherwise.
                  key="version"
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
                <EnvEditor
                  rows={env}
                  onChange={setEnv}
                  vars={Object.keys(project.data?.env ?? {}).sort()}
                  secretVars={project.data?.secrets}
                  databases={databases.map((d) => d.name)}
                  className="sm:col-span-2"
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

function EngineCard({
  selected,
  onSelect,
  icon,
  title,
  description,
}: {
  selected: boolean;
  onSelect: () => void;
  icon: ReactNode;
  title: string;
  description: string;
}) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={selected}
      onClick={onSelect}
      className={cn(
        "flex items-center gap-3 rounded-xl border p-3 text-left transition-colors outline-none hover:bg-muted/50 focus-visible:ring-3 focus-visible:ring-ring/50",
        selected && "border-foreground/40 bg-muted/50",
      )}
    >
      {icon}
      <span className="min-w-0">
        <span className="block text-sm font-medium">{title}</span>
        <span className="block truncate text-xs text-muted-foreground">{description}</span>
      </span>
    </button>
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
