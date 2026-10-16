import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, Box, Construction, ChevronLeft, Database, GitBranch, GitPullRequestArrow, Layers, Plus, Trash2, TriangleAlert } from "lucide-react";
import { lazy, Suspense, useEffect, useId, useState, type FormEvent, type ReactNode } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router";
import { toast } from "sonner";
import { ServiceIcon, ServiceIconTile } from "@/components/service-icon";
import { ChoiceTile, CopyButton, DangerZone, EmptyState, ErrorText, Mono, Section, StatCard, StateBadge, Tag, Loading } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { RenameCard } from "@/components/rename-card";
import { TagChips, TagFilter, TagsCard, useTagFilter } from "@/components/tags";
import { DomainField } from "@/components/domain-field";
import { EnvEditor } from "@/components/env-editor";
import { PageBody, PageHeader } from "@/components/page-header";
import { SaveBar } from "@/components/save-bar";
import { memoryOf, useUsage } from "@/components/usage";
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
import { useAccess } from "@/lib/access";
import { useTab } from "@/hooks/use-tab";
import { dbFields, dbRef, envMap, envRows, envSecrets, formatBytes, liveState, sameEnv, type EnvRow } from "@/lib/format";
import {
  api,
  canBackup,
  httpRouted,
  isDatabase,
  type DatabaseKind,
  type DatabaseRef,
  type Perm,
  type Project as ProjectT,
  type Service as ServiceT,
  type ServiceInput,
} from "../api";
import { BackupNowDialog } from "@/components/backup-now-dialog";
import { ComposeDialog } from "@/components/compose-dialog";
import { TemplateDialog } from "@/components/template-dialog";
import { ReachIcon, reachOf } from "@/components/reach";
import { GitSource, useProjectGit } from "@/components/git-source";
import { Previews } from "@/components/previews";
import { SleepCard } from "@/components/project-sleep";
import { MaintenanceCard, StartingCard } from "@/components/maintenance";
import { issueRing, issueText, needsAttention, serviceIssue } from "@/components/topology";
import { cn } from "@/lib/utils";

const MapCanvas = lazy(() => import("@/components/canvas/map-canvas"));

export function Project() {
  const { id = "" } = useParams();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const project = useQuery({ queryKey: ["project", id], queryFn: () => api.project(id) });
  const [tab, setTab] = useTab(["services", "map", "environment", "git", "previews", "settings"], "services");
  const git = useProjectGit(id);
  const gitManaged = !!git.data;
  const { can } = useAccess();
  const may = (p: Perm) => can(id, p);
  const editable = !gitManaged && may("settings");
  const services = useQuery({
    queryKey: ["services", id],
    queryFn: () => api.services(id),
    refetchInterval: 5_000,
  });

  const targets = useQuery({ queryKey: ["storage"], queryFn: api.backupTargets });
  const backupAll = async (targetId: string) => {
    const res = await api.backupProject(id, targetId);
    qc.invalidateQueries({ queryKey: ["backups"] });
    if (res.error) toast.warning("Some services were not backed up", { description: res.error });
  };
  const hasBackups = services.data?.some(canBackup);
  // Links like ?tab=settings#maintenance land on their card once it renders.
  const { hash } = useLocation();
  const settingsReady = tab === "settings" && !!project.data && !!services.data;
  useEffect(() => {
    if (settingsReady && hash) document.getElementById(hash.slice(1))?.scrollIntoView({ block: "start" });
  }, [settingsReady, hash]);
  const memory = memoryOf(useUsage().data, (u) => u.projectId === id);
  const rename = useMutation({
    meta: { error: "Couldn't rename the project" },
    mutationFn: (name: string) => api.renameProject(id, name),
    onSuccess: (p) => {
      qc.setQueryData(["project", id], p);
      qc.invalidateQueries({ queryKey: ["projects"] });
    },
  });
  const setTags = useMutation({
    meta: { error: "Couldn't save the tags" },
    mutationFn: (tags: string[]) => api.setProjectTags(id, tags),
    onSuccess: (p) => {
      qc.setQueryData(["project", id], p);
      qc.invalidateQueries({ queryKey: ["projects"] });
      qc.invalidateQueries({ queryKey: ["tags"] });
    },
  });
  const filter = useTagFilter();
  const remove = useMutation({
    meta: { error: "Couldn't delete the project" },
    mutationFn: (confirm: string) => api.deleteProject(id, confirm),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["projects"] });
      navigate("/projects");
    },
  });

  const parentId = project.data?.parentId;
  const parent = useQuery({ queryKey: ["project", parentId], queryFn: () => api.project(parentId!), enabled: !!parentId });
  const crumbs = [
    { label: "Projects", to: "/projects" },
    ...(parentId ? [{ label: parent.data?.name ?? <Spinner className="size-3.5" />, to: `/projects/${parentId}?tab=previews` }] : []),
    { label: project.data?.name ?? <Spinner className="size-3.5" /> },
  ];
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
  const apps = list.filter((s) => !isDatabase(s.kind));
  const dbs = list.flatMap((s) => (isDatabase(s.kind) ? [{ ...s, kind: s.kind }] : []));
  const shownApps = apps.filter((s) => filter.matches(s.tags));
  const shownDbs = dbs.filter((s) => filter.matches(s.tags));
  const states = list.map(liveState);
  const attention = list.filter(needsAttention).length;

  return (
    <>
      <PageHeader
        crumbs={crumbs}
        actions={
          project.data && (
            <>
              {gitManaged && (
                <Tag className="flex items-center gap-1">
                  <GitBranch className="size-3" />
                  Managed by git
                </Tag>
              )}
              {parentId && (
                <Tag className="flex items-center gap-1">
                  <GitPullRequestArrow className="size-3" />
                  Preview{project.data.preview?.branch ? ` of ${project.data.preview.branch}` : ""}
                </Tag>
              )}
              <ComposeDialog projectId={id} canImport={!gitManaged} />
              {editable && <TemplateDialog projectId={id} />}
              {editable && <NewServiceDialog projectId={id} />}
            </>
          )
        }
      />
      <PageBody>
        {project.data?.maintenance?.enabled && (
          <p role="status" className="flex flex-wrap items-center gap-x-2 text-sm text-amber-600 dark:text-amber-400">
            <Construction className="size-4" />
            Maintenance mode: visitors of the project&apos;s apps get the maintenance page.
            <Link to="?tab=settings#maintenance" replace className="underline underline-offset-4">
              Settings
            </Link>
          </p>
        )}
        {list.length > 0 && (
          <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
            <StatCard icon={Layers} label="Services" value={list.length} />
            <StatCard
              icon={Activity}
              label="Running"
              value={states.filter((s) => s === "running").length}
              hint={memory !== undefined && `${formatBytes(memory)} memory in use`}
              tone="good"
            />
            <StatCard icon={Database} label="Databases" value={dbs.length} />
            <StatCard
              icon={TriangleAlert}
              label="Need attention"
              value={attention}
              tone={attention > 0 ? "bad" : undefined}
            />
          </div>
        )}
        <Tabs value={tab} onValueChange={setTab}>
          <TabsList variant="line">
            <TabsTrigger value="services">Services</TabsTrigger>
            <TabsTrigger value="map">Map</TabsTrigger>
            <TabsTrigger value="environment">Environment</TabsTrigger>
            {may("git") && !parentId && <TabsTrigger value="git">Git</TabsTrigger>}
            {project.data && !parentId && <TabsTrigger value="previews">Previews</TabsTrigger>}
            {(may("settings") || may("deploy") || may("backups") || may("delete")) && <TabsTrigger value="settings">Settings</TabsTrigger>}
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
                description={
                  gitManaged
                    ? "The compose file lists none yet, or the first sync is running."
                    : editable
                      ? "Add an app from a Docker image, or a PostgreSQL database."
                      : undefined
                }
                action={editable && <NewServiceDialog projectId={id} />}
              />
            ) : (
              <>
                <TagFilter items={list.map((s) => s.tags)} filter={filter} />
                <ServiceGroup title="Apps" services={shownApps} />
                <ServiceGroup title="Databases" services={shownDbs} />
                {shownApps.length + shownDbs.length === 0 && (
                  <p className="py-6 text-center text-sm text-muted-foreground">No service has every selected tag.</p>
                )}
              </>
            )}
          </TabsContent>
          <TabsContent value="map" className="space-y-6">
            {tab === "map" && <ProjectMap projectId={id} />}
          </TabsContent>
          <TabsContent value="environment" className="space-y-6">
            {project.data && <SharedVariables key={project.data.id} project={project.data} canEdit={may("env")} />}
            <DatabaseReferences dbs={dbs} />
          </TabsContent>
          <TabsContent value="git" className="space-y-6">
            {tab === "git" && may("git") && !parentId && <GitSource projectId={id} />}
          </TabsContent>
          <TabsContent value="previews" className="space-y-6">
            {tab === "previews" && project.data && !parentId && (
              <Previews project={project.data} canDeploy={may("deploy")} canEdit={may("env")} />
            )}
          </TabsContent>
          <TabsContent value="settings" className="space-y-6">
            {project.data && may("settings") && (
              <RenameCard
                key={project.data.name}
                name={project.data.name}
                description="Containers are renamed in place; nothing restarts. Tokens and scripts that name the project need the new name."
                pending={rename.isPending}
                onRename={(name) => rename.mutate(name)}
              />
            )}
            {project.data && may("settings") && (
              <TagsCard
                tags={project.data.tags ?? []}
                description="They group projects on the projects page, which filters by them."
                pending={setTags.isPending}
                onSave={(tags) => setTags.mutate(tags)}
              />
            )}
            {project.data && ((services.data && may("settings")) || (may("deploy") && list.some(httpRouted))) && (
              // One card: the starting page is what a sleeping project shows.
              <div className="divide-y overflow-hidden rounded-xl ring-1 ring-foreground/10 *:data-[slot=card]:rounded-none *:data-[slot=card]:ring-0">
                {services.data && may("settings") && (
                  <SleepCard key={project.data.sleepAfterSeconds} project={project.data} services={services.data} />
                )}
                {may("deploy") && list.some(httpRouted) && <StartingCard key={`starting-${project.data.id}`} project={project.data} />}
              </div>
            )}
            {project.data && may("deploy") && list.some(httpRouted) && (
              <MaintenanceCard key={`maintenance-${project.data.id}`} project={project.data} />
            )}
            {hasBackups && may("backups") && (
              <Section
                id="backup"
                title="Back up data"
                description="Back up every database and app volume of the project now, to one storage."
                actions={
                  <BackupNowDialog
                    variant="outline"
                    what={`the data of ${project.data?.name ?? "the project"}`}
                    targets={targets.data ?? []}
                    onBackup={backupAll}
                  />
                }
              />
            )}
            {project.data && may("delete") && (
              <DangerZone description="Deleting the project destroys all its containers, databases and volumes.">
                <ConfirmDialog
                  trigger={
                    <Button variant="destructive" size="sm" disabled={remove.isPending}>
                      <Trash2 data-icon="inline-start" />
                      Delete project
                    </Button>
                  }
                  title={`Delete project ${project.data.name}?`}
                  description="All its containers, databases and volumes will be destroyed."
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
    <Suspense fallback={<Loading />}>
      <MapCanvas servers={topology.data.servers} projectId={projectId} className="h-[70vh] min-h-[28rem]" />
    </Suspense>
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
  const isDb = isDatabase(s.kind);
  const live = s.containers.filter((c) => !c.retired);
  const used = useUsage().data?.[s.id]?.memoryBytes;
  const problem = serviceIssue(s);
  return (
    <Link to={`/services/${s.id}`} className="group rounded-xl outline-none focus-visible:ring-3 focus-visible:ring-ring/50">
      <Card
        className={cn(
          "h-full gap-3 px-4 transition-all group-hover:-translate-y-px group-hover:shadow-md group-hover:ring-foreground/20",
          problem && issueRing[problem.tone],
        )}
      >
        <div className="flex items-start gap-3">
          <ServiceIconTile service={s} />
          <div className="min-w-0 flex-1">
            <p className="truncate font-medium">{s.name}</p>
            <p className="truncate font-mono text-xs text-muted-foreground">
              {s.image}
            </p>
          </div>
          <StateBadge state={liveState(s)} />
        </div>
        {problem && (
          <p title={problem.text} className={cn("flex items-center gap-1.5 text-xs", issueText[problem.tone])}>
            <TriangleAlert className="size-3.5 shrink-0" />
            <span className="truncate">{problem.short}</span>
          </p>
        )}
        {/* The first of what's pinned to the bottom takes the space. */}
        <TagChips tags={s.tags} className="mt-auto" />
        <div className={cn("flex flex-wrap items-center gap-1.5", !s.tags?.length && "mt-auto")}>
          {isDb ? (
            <>
              <Tag>
                {used !== undefined && `${formatBytes(used)} / `}
                {s.memoryMb} MB
              </Tag>
              {s.cpus > 0 && <Tag>{s.cpus} CPU</Tag>}
            </>
          ) : (
            <>
              <Tag className="flex items-center gap-1 font-mono font-normal">
                <ReachIcon service={s} className="size-3" />
                {reachOf(s).label}
              </Tag>
              <Tag>
                {live.filter((c) => c.state === "running").length}/{s.replicas} replica{s.replicas > 1 ? "s" : ""}
              </Tag>
              {used !== undefined && <Tag>{formatBytes(used)} memory</Tag>}
            </>
          )}
        </div>
      </Card>
    </Link>
  );
}

function SharedVariables({ project, canEdit }: { project: ProjectT; canEdit: boolean }) {
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
          readOnly={!canEdit}
        />
      </form>
      {canEdit && (
        <SaveBar form={formId} dirty={dirty} saving={save.isPending} onReset={() => setRows(envRows(project.env, project.secrets))} />
      )}
    </Section>
  );
}

/** The credentials each database offers to the services' env. */
function DatabaseReferences({ dbs }: { dbs: DatabaseRef[] }) {
  if (dbs.length === 0) return null;
  return (
    <Section
      title="Databases"
      description="Credentials a service can use in its environment, e.g. under Secrets → Connect database."
    >
      {dbs.map((db) => (
        <div key={db.name} className="space-y-1.5">
          <p className="flex items-center gap-2 text-sm font-medium">
            <ServiceIcon service={db} className="size-4" />
            {db.name}
          </p>
          <ul className="flex flex-wrap gap-1.5">
            {dbFields[db.kind].map((f) => (
              <li key={f} className="inline-flex items-center gap-0.5 rounded-md bg-muted py-0.5 pr-0.5 pl-2 font-mono text-xs">
                {dbRef(db.name, f)}
                <CopyButton value={dbRef(db.name, f)} label={`Copy ${f} reference`} />
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
  // Empty: the kind's default (its first version, its memory).
  version: "",
  memory: "",
};

type DatabaseChoice = {
  kind: DatabaseKind;
  title: string;
  /** Major versions offered, newest first. */
  versions: string[];
  version: string;
  image: (version: string) => string;
  memory: number;
  minMemory: number;
  memoryHint: string;
  placeholder: string;
};

// PostGIS's images are PostgreSQL's with the extension: the same kind, another image.
const postgisTags: Record<string, string> = { "18": "18-3.6", "17": "17-3.6", "16": "16-3.5", "15": "15-3.5" };

const databaseChoices: Record<DatabaseKind | "postgis", DatabaseChoice> = {
  postgres: {
    kind: "postgres",
    title: "PostgreSQL",
    versions: ["18", "17", "16", "15"],
    version: "17",
    image: (v) => `postgres:${v}-alpine`,
    memory: 512,
    minMemory: 128,
    memoryHint: "Container limit; shared_buffers is sized from it.",
    placeholder: "db",
  },
  postgis: {
    kind: "postgres",
    title: "PostGIS",
    versions: ["18", "17", "16", "15"],
    version: "17",
    // Alpine like postgres's own: an existing database can switch to it (same collations).
    image: (v) => `postgis/postgis:${postgisTags[v]}-alpine`,
    memory: 512,
    minMemory: 128,
    memoryHint: "Container limit; shared_buffers is sized from it.",
    placeholder: "db",
  },
  redis: {
    kind: "redis",
    title: "Redis",
    versions: ["8", "7"],
    version: "8",
    image: (v) => `redis:${v}-alpine`,
    memory: 256,
    minMemory: 32,
    memoryHint: "Container limit; Redis keeps its data within 75% of it.",
    placeholder: "cache",
  },
  mysql: {
    kind: "mysql",
    title: "MySQL",
    versions: ["9", "8.4"],
    version: "8.4",
    image: (v) => `mysql:${v}`,
    memory: 512,
    minMemory: 256,
    memoryHint: "Container limit; half of it goes to InnoDB's buffer pool.",
    placeholder: "mysql",
  },
  mariadb: {
    kind: "mariadb",
    title: "MariaDB",
    versions: ["11.8", "11.4", "10.11"],
    version: "11.8",
    image: (v) => `mariadb:${v}`,
    memory: 512,
    minMemory: 256,
    memoryHint: "Container limit; half of it goes to InnoDB's buffer pool.",
    placeholder: "mariadb",
  },
  mongodb: {
    kind: "mongodb",
    title: "MongoDB",
    // 8.0 refuses to start on Linux 6.19 and later.
    versions: ["8.2", "7.0"],
    version: "8.2",
    image: (v) => `mongo:${v}`,
    memory: 1024,
    minMemory: 512,
    memoryHint: "Container limit; WiredTiger's cache is sized from it.",
    placeholder: "mongo",
  },
};

type Choice = "image" | keyof typeof databaseChoices;

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
      ...dbChoice("postgres", "Relational database, backed up on a schedule."),
      ...dbChoice("postgis", "PostgreSQL with geographic types and queries."),
      ...dbChoice("mysql", "The most common relational database for web apps."),
      ...dbChoice("mariadb", "MySQL-compatible relational database."),
      ...dbChoice("mongodb", "Document database for JSON-shaped data."),
      ...dbChoice("redis", "In-memory store for caches, queues and sessions."),
    ],
  },
];

function dbChoice(id: keyof typeof databaseChoices, description: string) {
  const { kind, title, image, version } = databaseChoices[id];
  return [{ id, icon: <ServiceIcon service={{ kind, image: image(version) }} />, title, description }];
}

const choiceTitle = (c: Choice) => (c === "image" ? "New app from a Docker image" : `New ${databaseChoices[c].title} database`);

/** Creates a service in a project. stay keeps the user where they are (the map) instead of opening it. */
export function NewServiceDialog({ projectId, trigger, stay }: { projectId: string; trigger?: ReactNode; stay?: boolean }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const services = useQuery({ queryKey: ["services", projectId], queryFn: () => api.services(projectId) });
  const databases: DatabaseRef[] = services.data?.flatMap((s) => (isDatabase(s.kind) ? [{ name: s.name, kind: s.kind }] : [])) ?? [];
  const project = useQuery({ queryKey: ["project", projectId], queryFn: () => api.project(projectId) });

  const [open, setOpen] = useState(false);
  const [env, setEnv] = useState<EnvRow[]>([]);
  // First step: what to create; the form follows.
  const [choice, setChoice] = useState<Choice | null>(null);
  const db = !choice || choice === "image" ? null : databaseChoices[choice];
  const kind = db?.kind ?? "app";
  const [form, setForm] = useState(emptyForm);
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });

  const create = useMutation({
    meta: { error: "Couldn't create the service" },
    mutationFn: async (deploy: boolean) => {
      const input: ServiceInput =
        db
          ? {
              kind,
              name: form.name.trim(),
              image: db.image(form.version || db.version),
              memoryMb: Number(form.memory) || db.memory,
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
      if (deploy) await api.deploy(svc.id);
      return svc;
    },
    onSuccess: (svc) => {
      qc.invalidateQueries({ queryKey: ["services", projectId] });
      qc.invalidateQueries({ queryKey: ["topology"] });
      close();
      if (!stay) navigate(`/services/${svc.id}`);
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
    setOpen(true);
  };
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    create.mutate(true);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        {trigger ?? (
          <Button size="sm">
            <Plus data-icon="inline-start" />
            New service
          </Button>
        )}
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
              <DialogTitle>{choiceTitle(choice)}</DialogTitle>
              <DialogDescription>
                {kind === "app"
                  ? "Create it to set volumes, a pre-deploy command or health checks before its first deploy."
                  : "It is deployed as soon as it is created."}
              </DialogDescription>
            </DialogHeader>
            <div className="grid gap-4 sm:grid-cols-2">
              <FloatingInput
                label="Name"
                required
                autoFocus
                value={form.name}
                onChange={set("name")}
                placeholder={db ? db.placeholder : "web"}
                description={kind !== "app" ? "Also the hostname apps use to connect." : undefined}
              />
              {db ? (
                <>
                  <FloatingSelect
                    key={`version-${choice}`}
                    label="Version"
                    value={form.version || db.version}
                    onValueChange={(v) => setForm({ ...form, version: v })}
                    options={db.versions.map((v) => ({ value: v, label: `${db.title} ${v}` }))}
                  />
                  <FloatingInput
                    label="Memory (MB)"
                    type="number"
                    min={db.minMemory}
                    step={db.minMemory < 128 ? 64 : 128}
                    value={form.memory}
                    onChange={set("memory")}
                    placeholder={String(db.memory)}
                    description={db.memoryHint}
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
                    databases={databases}
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
              {kind === "app" && (
                <Button
                  type="button"
                  variant="outline"
                  disabled={create.isPending}
                  loading={create.isPending && !create.variables}
                  onClick={(e) => e.currentTarget.form?.reportValidity() && create.mutate(false)}
                >
                  Create
                </Button>
              )}
              <Button type="submit" disabled={create.isPending} loading={create.isPending && create.variables}>
                Create & deploy
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
