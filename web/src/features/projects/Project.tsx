import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, Construction, Database, GitBranch, GitPullRequestArrow, Layers, Trash2, TriangleAlert } from "lucide-react";
import { lazy, Suspense, useEffect } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router";
import { toast } from "sonner";
import { ErrorText, Tag } from "@/components/common";
import { DangerZone } from "@kipit-labs/kipitidy/danger-zone";
import { EmptyState } from "@kipit-labs/kipitidy/empty-state";
import { Loading } from "@kipit-labs/kipitidy/loading";
import { Section } from "@kipit-labs/kipitidy/section";
import { StatCard } from "@kipit-labs/kipitidy/stat-card";
import { ConfirmDialog } from "@kipit-labs/kipitidy/confirm-dialog";
import { RenameCard } from "@/features/services/rename-card";
import { TagFilter, TagsCard, useTagFilter } from "@/components/tags";
import { PageBody, PageHeader } from "@/features/layout/page-header";
import { memoryOf, useUsage } from "@/features/services/usage";
import { Button } from "@kipit-labs/kipitidy/button";
import { Spinner } from "@kipit-labs/kipitidy/spinner";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@kipit-labs/kipitidy/tabs";
import { useAccess } from "@/lib/access";
import { useTab } from "@/hooks/use-tab";
import { formatBytes, liveState } from "@/lib/format";
import { api, canBackup, httpRouted, isDatabase, type Perm } from "../../api";
import { BackupNowDialog } from "@/features/backups/backup-now-dialog";
import { ComposeDialog } from "@/features/projects/compose-dialog";
import { TemplateDialog } from "@/features/projects/template-dialog";
import { GitSource, useProjectGit } from "@/features/projects/git-source";
import { Previews } from "@/features/projects/previews";
import { SleepCard } from "@/features/projects/project-sleep";
import { MaintenanceCard, StartingCard } from "@/features/services/maintenance";
import { needsAttention } from "@/features/map/topology";
import { NewServiceDialog } from "@/features/projects/new-service-dialog";
import { DatabaseReferences, SharedVariables } from "@/features/projects/project-environment";
import { ServiceGroup } from "@/features/projects/project-services";

const MapCanvas = lazy(() => import("@/features/map/canvas/map-canvas"));

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
