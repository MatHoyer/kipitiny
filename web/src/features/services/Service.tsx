import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, Construction, ExternalLink, Globe, Layers, Play, Rocket, RotateCcw, Square, Trash2, TriangleAlert } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { portLabel } from "@/features/services/reach";
import { ServiceIcon } from "@/components/service-icon";
import { Empty, ErrorText, StateBadge } from "@/components/common";
import { DangerZone } from "@kipit-labs/kipitidy/danger-zone";
import { Loading } from "@kipit-labs/kipitidy/loading";
import { Section } from "@kipit-labs/kipitidy/section";
import { StatCard } from "@kipit-labs/kipitidy/stat-card";
import { ConfirmDialog } from "@kipit-labs/kipitidy/confirm-dialog";
import { DataTab } from "@/features/data/data-tab";
import { PageBody, PageHeader } from "@/features/layout/page-header";
import { JobsTab } from "@/features/services/jobs";
import { LiveLogs } from "@/features/services/logs";
import { UptimeSection } from "@/features/services/uptime";
import { ServiceUsageSection } from "@/features/services/usage";
import { Button } from "@kipit-labs/kipitidy/button";
import { Spinner } from "@kipit-labs/kipitidy/spinner";
import { Card } from "@kipit-labs/kipitidy/card";
import { AnalyticsTab } from "@/features/analytics/analytics-tab";
import { DeployErrorsBanner, ErrorsTab } from "@/features/services/errors-tab";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@kipit-labs/kipitidy/tabs";
import { useAccess } from "@/lib/access";
import { FilesTab } from "@/features/files/files-tab";
import { useTab } from "@/hooks/use-tab";
import { serviceState, timeAgo } from "@/lib/format";
import { cn } from "@/lib/utils";
import { issueText, serviceIssue } from "@/features/map/topology";
import { api, canBackup, httpRouted, isDatabase, type Perm } from "../../api";
import { ServiceSettings } from "@/features/services/settings";
import { dnsOnlyHint } from "@/features/services/settings/networking";
import { GitManagedNote } from "@/features/services/settings/parts";
import { ComposeDialog } from "@/features/projects/compose-dialog";
import { useProjectGit } from "@/features/projects/git-source";
import { BackupsCard } from "@/features/services/backups-card";
import { Deployments } from "@/features/services/deployments";
import { EnvironmentCard } from "@/features/services/environment-card";
import { ConnectionCard, ContainerRow } from "@/features/services/overview";
import { ServiceTerminal } from "@/features/services/service-terminal";

export function Service() {
  const { id = "" } = useParams();
  const qc = useQueryClient();
  const navigate = useNavigate();

  const service = useQuery({ queryKey: ["service", id], queryFn: () => api.service(id), refetchInterval: 5_000 });
  const project = useQuery({
    queryKey: ["project", service.data?.projectId],
    queryFn: () => api.project(service.data!.projectId),
    enabled: !!service.data,
  });
  const deployments = useQuery({
    queryKey: ["deployments", id],
    queryFn: () => api.deployments(id),
    refetchInterval: (q) => (q.state.data?.some((d) => d.status === "running") ? 1_000 : 10_000),
  });
  const deploying = deployments.data?.some((d) => d.status === "running") ?? false;
  const [selected, setSelected] = useState<string | null>(null);
  const [tab, setTab] = useTab(["overview", "deployments", "logs", "terminal", "data", "files", "environment", "jobs", "backups", "analytics", "errors", "settings"], "overview");
  const git = useProjectGit(service.data?.projectId ?? "");
  const gitManaged = !!git.data;
  const { can } = useAccess();
  const may = (p: Perm) => can(service.data?.projectId, p);

  // Refresh containers as soon as a deploy finishes.
  const wasDeploying = useRef(false);
  useEffect(() => {
    if (wasDeploying.current && !deploying) qc.invalidateQueries({ queryKey: ["service", id] });
    wasDeploying.current = deploying;
  }, [deploying, id, qc]);

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["service", id] });
    qc.invalidateQueries({ queryKey: ["deployments", id] });
  };
  const deploy = useMutation({
    meta: { error: "Couldn't start the deployment" },
    mutationFn: () => api.deploy(id),
    onSuccess: (d) => {
      setSelected(d.id);
      setTab("deployments");
      refresh();
    },
  });
  const action = useMutation({
    meta: { error: "Couldn't change the service state" },
    mutationFn: (a: "start" | "stop" | "restart") => api.serviceAction(id, a),
    onSuccess: (svc) => qc.setQueryData(["service", id], svc),
  });
  const remove = useMutation({
    meta: { error: "Couldn't delete the service" },
    mutationFn: (confirm: string) => api.deleteService(id, confirm),
    onSuccess: () => navigate(`/projects/${service.data?.projectId}`),
  });

  const svc = service.data;
  const crumbs = [
    { label: "Projects", to: "/projects" },
    { label: project.data?.name ?? <Spinner className="size-3.5" />, to: svc && `/projects/${svc.projectId}` },
    { label: svc?.name ?? <Spinner className="size-3.5" /> },
  ];
  if (!svc)
    return (
      <>
        <PageHeader crumbs={crumbs} />
        <PageBody>{service.error ? <ErrorText error={service.error} /> : <Loading />}</PageBody>
      </>
    );

  const active = svc.containers.filter((c) => !c.retired);
  const state = deploying ? "deploying" : svc.preview === "only" ? "previews only" : svc.stopped ? "stopped" : svc.sleeping ? "asleep" : serviceState(active);
  const allStopped = active.length > 0 && active.every((c) => c.state !== "running");
  const problem = serviceIssue(svc);
  const busy = deploying || deploy.isPending || action.isPending || remove.isPending;
  const isDb = isDatabase(svc.kind);
  const hasData = isDb || svc.volumes.length > 0;
  const running = active.filter((c) => c.state === "running").length;
  const last = deployments.data?.[0];

  return (
    <>
      <PageHeader
        crumbs={crumbs}
        actions={
          <>
            <ComposeDialog projectId={svc.projectId} service={svc.name} />
            {svc.preview === "only" && <span className="text-sm text-muted-foreground">Runs in previews only</span>}
            {svc.preview !== "only" && svc.containers.length > 0 && may("deploy") && (
              <>
                <Button variant="outline" size="sm" disabled={busy} onClick={() => action.mutate("restart")}>
                  <RotateCcw data-icon="inline-start" />
                  Restart
                </Button>
                <Button variant="outline" size="sm" disabled={busy} onClick={() => action.mutate(allStopped ? "start" : "stop")}>
                  {allStopped ? <Play data-icon="inline-start" /> : <Square data-icon="inline-start" />}
                  {allStopped ? "Start" : "Stop"}
                </Button>
              </>
            )}
            {svc.preview !== "only" && may("deploy") && (
              <Button size="sm" loading={deploying} disabled={busy} onClick={() => deploy.mutate()}>
                <Rocket data-icon="inline-start" />
                {deploying ? "Deploying" : "Deploy"}
              </Button>
            )}
          </>
        }
      />
      <PageBody>
        {project.data?.maintenance?.enabled && svc.domain && (
          <p role="status" className="flex flex-wrap items-center gap-x-2 text-sm text-amber-600 dark:text-amber-400">
            <Construction className="size-4" />
            Maintenance mode: visitors of the project&apos;s apps get the maintenance page.
            <Link to={`/projects/${svc.projectId}?tab=settings#maintenance`} className="underline underline-offset-4">
              Project settings
            </Link>
          </p>
        )}
        {problem && (
          <div role="alert" className={cn("flex flex-wrap items-center gap-x-2 gap-y-1 text-sm", issueText[problem.tone])}>
            <p className="flex min-w-0 items-start gap-2">
              <TriangleAlert className="mt-0.5 size-4 shrink-0" />
              <span>{problem.short === problem.text ? problem.text : `${problem.short}: ${problem.text}`}</span>
            </p>
            {svc.routePending && may("deploy") && (
              <Button size="xs" variant="outline" loading={deploying} disabled={busy} onClick={() => deploy.mutate()} className="shrink-0">
                <Rocket data-icon="inline-start" />
                Deploy now
              </Button>
            )}
          </div>
        )}
        <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
          <Card size="sm" className="gap-1.5 px-3">
            <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <ServiceIcon service={svc} fallback={Activity} className="size-3.5" />
              State
            </p>
            <StateBadge state={state} className="self-start text-sm text-foreground" />
          </Card>
          <StatCard
            icon={Layers}
            label="Replicas"
            value={`${running}/${isDb ? 1 : svc.replicas}`}
            hint="running"
            tone={active.length > 0 && running < (isDb ? 1 : svc.replicas) ? "warn" : undefined}
          />
          <StatCard
            icon={Rocket}
            label="Last deploy"
            value={last ? timeAgo(last.createdAt) : "never"}
            hint={last && <StateBadge state={last.status} className="border-0 p-0" />}
          />
          <Card size="sm" className="gap-1 px-3">
            <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <Globe className="size-3.5" />
              {isDb ? "Host" : "Domain"}
            </p>
            {isDb ? (
              <p className="truncate font-mono text-sm">{svc.name}</p>
            ) : svc.domain && !httpRouted(svc) ? (
              <>
                <p className="truncate font-mono text-sm">
                  {svc.domain}
                  {svc.publishedPorts[0] && `:${svc.publishedPorts[0].hostPort}`}
                </p>
                <p className="text-xs text-muted-foreground">{dnsOnlyHint(svc.publishedPorts)}</p>
              </>
            ) : svc.domain ? (
              <a
                href={`https://${svc.domain}`}
                target="_blank"
                rel="noreferrer"
                className="inline-flex min-w-0 items-center gap-1 text-sm font-medium underline-offset-4 hover:underline"
              >
                <span className="truncate">{svc.domain}</span>
                <ExternalLink className="size-3.5 shrink-0" />
              </a>
            ) : svc.publishedPorts.length > 0 ? (
              <p className="truncate font-mono text-sm">{svc.publishedPorts.map(portLabel).join(", ")}</p>
            ) : svc.hostNetwork ? (
              <p className="text-sm">host network</p>
            ) : (
              <p className="text-sm text-muted-foreground">private</p>
            )}
            {svc.dns?.state === "synced" && <p className="text-xs text-muted-foreground">DNS managed by kipitiny</p>}
          </Card>
        </div>

        <Tabs value={tab} onValueChange={setTab}>
          <TabsList variant="line" className="max-w-full justify-start overflow-x-auto overflow-y-hidden pb-1.5 [scrollbar-width:none]">
            <TabsTrigger value="overview">Overview</TabsTrigger>
            <TabsTrigger value="deployments">Deployments</TabsTrigger>
            <TabsTrigger value="logs">Logs</TabsTrigger>
            {may("terminal") && <TabsTrigger value="terminal">Terminal</TabsTrigger>}
            {hasData && may("data") && <TabsTrigger value="data">Data</TabsTrigger>}
            {may("files") && <TabsTrigger value="files">Files</TabsTrigger>}
            <TabsTrigger value="environment">Environment</TabsTrigger>
            {svc.kind === "app" && <TabsTrigger value="jobs">Jobs</TabsTrigger>}
            {canBackup(svc) && <TabsTrigger value="backups">Backups</TabsTrigger>}
            {svc.analytics && <TabsTrigger value="analytics">Analytics</TabsTrigger>}
            {svc.errors && <TabsTrigger value="errors">Errors</TabsTrigger>}
            {(may("settings") || may("delete")) && <TabsTrigger value="settings">Settings</TabsTrigger>}
          </TabsList>
          <TabsContent value="overview" className="space-y-6">
            {svc.containers.length > 0 && <ServiceUsageSection serviceId={svc.id} />}
            {svc.kind === "app" && <UptimeSection svc={svc} gitPath={git.data?.path} />}
            <Section title="Containers">
              {svc.containers.length === 0 ? (
                <Empty>Not deployed yet.</Empty>
              ) : (
                <ul className="-mx-3 -my-2 divide-y">
                  {svc.containers.map((c) => (
                    <ContainerRow key={c.id} serviceId={svc.id} container={c} />
                  ))}
                </ul>
              )}
            </Section>
            {isDatabase(svc.kind) && may("env") && <ConnectionCard serviceId={svc.id} host={svc.name} kind={svc.kind} />}
          </TabsContent>
          <TabsContent value="deployments">
            {svc.errors && <DeployErrorsBanner svc={svc} canRollback={may("deploy")} onIssues={() => setTab("errors")} />}
            {deployments.isPending ? (
              <Loading />
            ) : (
              <Deployments svc={svc} deployments={deployments.data ?? []} focus={selected} onFocus={setSelected} busy={busy} />
            )}
          </TabsContent>
          <TabsContent value="logs">
            {svc.containers.length === 0 ? (
              <Empty>Not deployed yet.</Empty>
            ) : (
              // Remount (and reconnect) whenever the set of containers changes.
              <LiveLogs key={active.map((c) => c.id).join()} serviceId={id} name={svc.name} />
            )}
          </TabsContent>
          {may("terminal") && (
            <TabsContent value="terminal">
              <ServiceTerminal svc={svc} />
            </TabsContent>
          )}
          {hasData && may("data") && (
            <TabsContent value="data">
              <DataTab svc={svc} />
            </TabsContent>
          )}
          {may("files") && (
            <TabsContent value="files">
              <FilesTab svc={svc} onBackups={() => setTab("backups")} />
            </TabsContent>
          )}
          {svc.analytics && (
            <TabsContent value="analytics">
              <AnalyticsTab svc={svc} />
            </TabsContent>
          )}
          {svc.errors && (
            <TabsContent value="errors">
              <ErrorsTab svc={svc} canChange={may("deploy")} />
            </TabsContent>
          )}
          <TabsContent value="environment" className="flex flex-col gap-6">
            {gitManaged && <GitManagedNote path={git.data!.path} />}
            <EnvironmentCard key={svc.id} svc={svc} gitManaged={gitManaged} canEdit={may("env")} />
          </TabsContent>
          {svc.kind === "app" && (
            <TabsContent value="jobs">
              <JobsTab svc={svc} canEdit={may("settings")} canRun={may("deploy")} gitPath={git.data?.path} />
            </TabsContent>
          )}
          {canBackup(svc) && (
            <TabsContent value="backups">
              <BackupsCard
                serviceId={svc.id}
                kind={svc.kind}
                name={project.data ? `${project.data.name}/${svc.name}` : svc.name}
                canBackUp={may("backups")}
              />
            </TabsContent>
          )}
          <TabsContent value="settings">
            <ServiceSettings
              svc={svc}
              projectName={project.data?.name}
              preview={!!project.data?.parentId}
              gitPath={git.data?.path}
              danger={
                may("delete") && (
                  <DangerZone
                    description={
                      isDb
                        ? "Deleting the database destroys its data volume."
                        : hasData
                          ? "Deleting the service removes its containers and destroys its volumes."
                          : "Deleting the service removes its containers."
                    }
                  >
                    <ConfirmDialog
                      trigger={
                        <Button variant="destructive" size="sm" disabled={busy || (gitManaged && !svc.orphaned)}>
                          <Trash2 data-icon="inline-start" />
                          {isDb ? "Delete database" : "Delete service"}
                        </Button>
                      }
                      title={isDb ? `Delete database ${svc.name}?` : `Delete service ${svc.name}?`}
                      description={
                        isDb
                          ? "Its data volume will be destroyed."
                          : hasData
                            ? "Its containers are removed and its volumes destroyed."
                            : "Its containers are removed."
                      }
                      typeToConfirm={hasData ? svc.name : undefined}
                      onConfirm={(name) => remove.mutate(name)}
                    />
                  </DangerZone>
                )
              }
            />
          </TabsContent>
        </Tabs>
      </PageBody>
    </>
  );
}
