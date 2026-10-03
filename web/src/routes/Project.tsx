import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, Box, ChevronLeft, Database, Globe, Layers, Lock, Plus, Trash2, TriangleAlert } from "lucide-react";
import { useId, useState, type FormEvent, type ReactNode } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { toast } from "sonner";
import { PostgresIcon } from "@/components/brand-icons";
import { ChoiceTile, CopyButton, DangerZone, EmptyState, ErrorText, IconTile, Mono, Section, StatCard, StateBadge, Tag, Loading } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DomainField } from "@/components/domain-field";
import { EnvEditor } from "@/components/env-editor";
import { PageBody, PageHeader } from "@/components/page-header";
import { MapLegend, ServerCard } from "@/components/topology";
import { SaveBar } from "@/components/save-bar";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { Card } from "@/components/ui/card";
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
import { useTab } from "@/hooks/use-tab";
import { dbFields, dbRef, envMap, envRows, envSecrets, liveState, sameEnv, troubled, type EnvRow } from "@/lib/format";
import { api, type Project as ProjectT, type Service as ServiceT, type ServiceInput } from "../api";
import { BackupNowDialog } from "@/components/backup-now-dialog";

export function Project() {
  const { id = "" } = useParams();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const project = useQuery({ queryKey: ["project", id], queryFn: () => api.project(id) });
  const [tab, setTab] = useTab(["services", "map", "environment", "settings"], "services");
  const services = useQuery({
    queryKey: ["services", id],
    queryFn: () => api.services(id),
    refetchInterval: 5_000,
  });

  const targets = useQuery({ queryKey: ["storage"], queryFn: api.backupTargets });
  const backupAll = async (targetId: string) => {
    const res = await api.backupProject(id, targetId);
    qc.invalidateQueries({ queryKey: ["backups"] });
    if (res.error) toast.warning("Some databases were not backed up", { description: res.error });
  };
  const hasDatabases = services.data?.some((s) => s.kind === "postgres");
  const remove = useMutation({
    meta: { error: "Couldn't delete the project" },
    mutationFn: (confirm: string) => api.deleteProject(id, confirm),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["projects"] });
      navigate("/");
    },
  });

  const crumbs = [{ label: "Projects", to: "/" }, { label: project.data?.name ?? <Spinner className="size-3.5" /> }];
  if (project.error)
    return (
      <>
        <PageHeader crumbs={crumbs} />
        <PageBody>
          <ErrorText error={project.error} />
        </PageBody>
      </>
    );

  const list = services.data ?? [];
  const apps = list.filter((s) => s.kind !== "postgres");
  const dbs = list.filter((s) => s.kind === "postgres");
  const states = list.map(liveState);

  return (
    <>
      <PageHeader crumbs={crumbs} actions={project.data && <NewServiceDialog projectId={id} />} />
      <PageBody>
        {list.length > 0 && (
          <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
            <StatCard icon={Layers} label="Services" value={list.length} />
            <StatCard icon={Activity} label="Running" value={states.filter((s) => s === "running").length} tone="good" />
            <StatCard icon={Database} label="Databases" value={dbs.length} />
            <StatCard
              icon={TriangleAlert}
              label="Need attention"
              value={states.filter(troubled).length}
              tone={states.some(troubled) ? "bad" : undefined}
            />
          </div>
        )}
        <Tabs value={tab} onValueChange={setTab}>
          <TabsList variant="line">
            <TabsTrigger value="services">Services</TabsTrigger>
            <TabsTrigger value="map">Map</TabsTrigger>
            <TabsTrigger value="environment">Environment</TabsTrigger>
            <TabsTrigger value="settings">Settings</TabsTrigger>
          </TabsList>
          <TabsContent value="services" className="space-y-6">
            {services.error ? (
              <ErrorText error={services.error} />
            ) : !services.data ? (
              <Loading />
            ) : list.length === 0 ? (
              <EmptyState
                icon={Layers}
                title="No services yet"
                description="Add an app from a Docker image, or a PostgreSQL database."
                action={<NewServiceDialog projectId={id} />}
              />
            ) : (
              <>
                <ServiceGroup title="Apps" services={apps} />
                <ServiceGroup title="Databases" services={dbs} />
              </>
            )}
          </TabsContent>
          <TabsContent value="map" className="space-y-6">
            {tab === "map" && <ProjectMap projectId={id} />}
          </TabsContent>
          <TabsContent value="environment" className="space-y-6">
            {project.data && <SharedVariables key={project.data.id} project={project.data} />}
            <DatabaseReferences names={dbs.map((s) => s.name)} />
          </TabsContent>
          <TabsContent value="settings" className="space-y-6">
            {hasDatabases && (
              <Section
                title="Back up databases"
                description="Back up every database of the project now, to one storage."
                actions={
                  <BackupNowDialog
                    variant="outline"
                    what={`every database of ${project.data?.name ?? "the project"}`}
                    targets={targets.data ?? []}
                    onBackup={backupAll}
                  />
                }
              />
            )}
            {project.data && (
              <DangerZone description="Deleting the project destroys all its containers and database data.">
                <ConfirmDialog
                  trigger={
                    <Button variant="destructive" size="sm" disabled={remove.isPending}>
                      <Trash2 data-icon="inline-start" />
                      Delete project
                    </Button>
                  }
                  title={`Delete project ${project.data.name}?`}
                  description="All its containers and database data will be destroyed."
                  typeToConfirm={project.data.name}
                  onConfirm={(name) => remove.mutate(name)}
                />
              </DangerZone>
            )}
          </TabsContent>
        </Tabs>
      </PageBody>
    </>
  );
}

function ProjectMap({ projectId }: { projectId: string }) {
  const topology = useQuery({
    queryKey: ["topology", projectId],
    queryFn: () => api.topology(projectId),
    refetchInterval: 5_000,
  });
  if (topology.error) return <ErrorText error={topology.error} />;
  if (!topology.data) return <Loading />;
  return (
    <>
      <MapLegend />
      {topology.data.servers.map((s) => (
        <ServerCard key={s.id} server={s} showName={false} />
      ))}
    </>
  );
}

function ServiceGroup({ title, services }: { title: string; services: ServiceT[] }) {
  if (services.length === 0) return null;
  return (
    <section className="space-y-2">
      <h2 className="text-xs font-medium tracking-wide text-muted-foreground uppercase">{title}</h2>
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {services.map((s) => (
          <ServiceCard key={s.id} svc={s} />
        ))}
      </div>
    </section>
  );
}

function ServiceCard({ svc: s }: { svc: ServiceT }) {
  const isDb = s.kind === "postgres";
  const live = s.containers.filter((c) => !c.retired);
  return (
    <Link to={`/services/${s.id}`} className="group rounded-xl outline-none focus-visible:ring-3 focus-visible:ring-ring/50">
      <Card className="h-full gap-3 px-4 transition-all group-hover:-translate-y-px group-hover:shadow-md group-hover:ring-foreground/20">
        <div className="flex items-start gap-3">
          {isDb ? (
            <span className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-[#4169E1]/10">
              <PostgresIcon aria-label="PostgreSQL" className="size-5 text-[#4169E1]" />
            </span>
          ) : (
            <IconTile icon={Box} />
          )}
          <div className="min-w-0 flex-1">
            <p className="truncate font-medium">{s.name}</p>
            <p className="truncate font-mono text-xs text-muted-foreground">
              {s.image}
            </p>
          </div>
          <StateBadge state={liveState(s)} />
        </div>
        <div className="mt-auto flex flex-wrap items-center gap-1.5">
          {isDb ? (
            <Tag>{s.memoryMb} MB</Tag>
          ) : (
            <>
              <Tag className="flex items-center gap-1 font-mono font-normal">
                {s.domain ? <Globe className="size-3" /> : <Lock className="size-3" />}
                {s.domain || "private"}
              </Tag>
              <Tag>
                {live.filter((c) => c.state === "running").length}/{s.replicas} replica{s.replicas > 1 ? "s" : ""}
              </Tag>
            </>
          )}
        </div>
      </Card>
    </Link>
  );
}

function SharedVariables({ project }: { project: ProjectT }) {
  const qc = useQueryClient();
  const [rows, setRows] = useState<EnvRow[]>(() => envRows(project.env, project.secrets));
  const dirty = !sameEnv(rows, project.env, project.secrets);
  const formId = useId();
  const save = useMutation({
    meta: { error: "Couldn't save the shared variables" },
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
          Available to every service of the project as <Mono>{"{{ project.NAME }}"}</Mono>. Values can be password
          manager secrets, e.g. <Mono>{"{{ pass://Vault/Item/field }}"}</Mono>.
        </>
      }
    >
      <form id={formId} onSubmit={onSubmit} className="space-y-4">
        <EnvEditor
          showLabel={false}
          rows={rows}
          onChange={setRows}
          passwordManagers
          description="An entry used by a service can't be removed."
        />
      </form>
      <SaveBar form={formId} dirty={dirty} saving={save.isPending} onReset={() => setRows(envRows(project.env, project.secrets))} />
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
};

type Choice = "image" | "postgres";

const choiceGroups: { label: string; choices: { id: Choice; icon: ReactNode; title: string; description: string }[] }[] = [
  {
    label: "Apps",
    choices: [
      { id: "image", icon: <Box />, title: "Docker image", description: "Run a published image." },
    ],
  },
  {
    label: "Databases",
    choices: [
      {
        id: "postgres",
        icon: <PostgresIcon className="text-[#4169E1]" />,
        title: "PostgreSQL",
        description: "Relational database, backed up on a schedule.",
      },
    ],
  },
];

const choiceTitles: Record<Choice, string> = {
  image: "New app from a Docker image",
  postgres: "New PostgreSQL database",
};

function NewServiceDialog({ projectId }: { projectId: string }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const services = useQuery({ queryKey: ["services", projectId], queryFn: () => api.services(projectId) });
  const databases = services.data?.filter((s) => s.kind === "postgres") ?? [];
  const project = useQuery({ queryKey: ["project", projectId], queryFn: () => api.project(projectId) });

  const [open, setOpen] = useState(false);
  const [env, setEnv] = useState<EnvRow[]>([]);
  // First step: what to create; the form follows.
  const [choice, setChoice] = useState<Choice | null>(null);
  const kind = choice === "postgres" ? "postgres" : "app";
  const [form, setForm] = useState(emptyForm);
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });

  const create = useMutation({
    meta: { error: "Couldn't create the service" },
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
              image: form.image.trim(),
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

  const close = () => {
    setOpen(false);
    setForm(emptyForm);
    setEnv([]);
    setChoice(null);
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
        {!choice ? (
          <>
            <DialogHeader>
              <DialogTitle>New service</DialogTitle>
              <DialogDescription>What do you want to run?</DialogDescription>
            </DialogHeader>
            {choiceGroups.map((g) => (
              <div key={g.label} className="space-y-2">
                <h3 className="text-xs font-medium tracking-wide text-muted-foreground uppercase">{g.label}</h3>
                <div className="grid gap-3 sm:grid-cols-3">
                  {g.choices.map((c) => (
                    <ChoiceTile key={c.id} icon={c.icon} title={c.title} description={c.description} onClick={() => setChoice(c.id)} />
                  ))}
                </div>
              </div>
            ))}
          </>
        ) : (
          <form onSubmit={onSubmit} className="contents">
            <DialogHeader>
              <DialogTitle>{choiceTitles[choice]}</DialogTitle>
              <DialogDescription>It is deployed as soon as it is created.</DialogDescription>
            </DialogHeader>
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
                  <FloatingInput
                    label="Image"
                    required
                    value={form.image}
                    onChange={set("image")}
                    placeholder="ghcr.io/org/app:latest"
                    className="sm:col-span-2"
                  />
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
            <DialogFooter>
              <Button type="button" variant="ghost" disabled={create.isPending} onClick={() => setChoice(null)}>
                <ChevronLeft data-icon="inline-start" />
                Back
              </Button>
              <Button type="submit" loading={create.isPending}>
                Create & deploy
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
