import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  ChevronRight,
  CircleCheck,
  CircleSlash,
  CircleX,
  Construction,
  ExternalLink,
  GitCommitHorizontal,
  Globe,
  Layers,
  Play,
  RefreshCw,
  RotateCcw,
  Rocket,
  Square,
  SquareTerminal,
  Trash2,
  TriangleAlert,
  Workflow,
} from "lucide-react";
import { lazy, Suspense, useEffect, useId, useRef, useState, type FormEvent, type ReactNode } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { toast } from "sonner";
import { portLabel } from "@/components/reach";
import { ServiceIcon } from "@/components/service-icon";
import { CopyButton, DangerZone, Empty, EmptyState, ErrorText, Mono, SecretList, Section, StatCard, StateBadge, Tag, Loading } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DataTab } from "@/components/data/data-tab";
import { EnvEditor } from "@/components/env-editor";
import { PageBody, PageHeader } from "@/components/page-header";
import { DeploymentLogView, LiveLogs } from "@/components/logs";
import { SaveBar } from "@/components/save-bar";
import { UptimeSection } from "@/components/uptime";
import { ServiceUsageSection, UsageGrid, useServiceStats } from "@/components/usage";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { Card } from "@/components/ui/card";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { AnalyticsTab } from "@/components/analytics-tab";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useAccess } from "@/lib/access";
import { FilesTab } from "@/components/files/files-tab";
import { useTab } from "@/hooks/use-tab";
import { byDay, envMap, envRows, envSecrets, formatBytes, formatCpu, formatDuration, sameEnv, serviceState, timeAgo, type EnvRow } from "@/lib/format";
import { cn } from "@/lib/utils";
import { issueText, serviceIssue } from "@/components/topology";
import { api, canBackup, databasePorts, databaseUrlKey, httpRouted, isDatabase, type Connection, type Container as ContainerT, type DatabaseKind, type Deployment, type Perm, type Service as ServiceT } from "../api";
import { BackupList } from "./BackupList";
import { Schedules } from "./Schedules";
import { BackupNowDialog } from "@/components/backup-now-dialog";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { ServiceSettings } from "@/components/service-settings";
import { dnsOnlyHint } from "@/components/service-settings/networking";
import { GitManagedNote } from "@/components/service-settings/parts";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { ComposeDialog } from "@/components/compose-dialog";
import { useProjectGit } from "@/components/git-source";

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
  const [tab, setTab] = useTab(["overview", "deployments", "logs", "terminal", "data", "files", "environment", "backups", "analytics", "settings"], "overview");
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
  const state = deploying ? "deploying" : svc.stopped ? "stopped" : svc.sleeping ? "asleep" : serviceState(active);
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
            {svc.containers.length > 0 && may("deploy") && (
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
            {may("deploy") && (
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
            {isDb && may("data") && <TabsTrigger value="data">Data</TabsTrigger>}
            {may("files") && <TabsTrigger value="files">Files</TabsTrigger>}
            <TabsTrigger value="environment">Environment</TabsTrigger>
            {canBackup(svc) && <TabsTrigger value="backups">Backups</TabsTrigger>}
            {svc.analytics && <TabsTrigger value="analytics">Analytics</TabsTrigger>}
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
          {isDb && may("data") && (
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
          <TabsContent value="environment" className="flex flex-col gap-6">
            {gitManaged && <GitManagedNote path={git.data!.path} />}
            <EnvironmentCard key={svc.id} svc={svc} gitManaged={gitManaged} canEdit={may("env")} />
          </TabsContent>
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

/** One container; opening it shows its own share of the service's usage. */
function ContainerRow({ serviceId, container: c }: { serviceId: string; container: ContainerT }) {
  const stats = useServiceStats(serviceId).data?.containers[c.id];
  const cur = stats?.current;
  return (
    <Collapsible asChild>
      <li className={cn("data-[state=open]:bg-muted/30", c.retired && "opacity-50")}>
        <CollapsibleTrigger className="group flex w-full flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-left outline-none focus-visible:bg-muted/50">
          <ChevronRight className="size-4 shrink-0 text-muted-foreground transition-transform group-data-[state=open]:rotate-90" />
          <span className="font-mono text-xs">{c.name}</span>
          <StateBadge state={c.health ?? c.state} />
          {cur && (
            <span className="text-xs text-muted-foreground tabular-nums">
              {formatCpu(cur.cpu)} · {formatBytes(cur.memoryBytes)}
            </span>
          )}
          <span className="flex-1 text-right text-xs text-muted-foreground">
            {c.retired ? "previous deploy, kept for its logs" : c.status}
          </span>
        </CollapsibleTrigger>
        <CollapsibleContent>
          <div className="px-3 pt-1 pb-3">
            {cur ? (
              <UsageGrid current={cur} history={stats.history} />
            ) : (
              <p className="text-sm text-muted-foreground">{c.state === "running" ? "No usage sampled yet." : "Not running."}</p>
            )}
          </div>
        </CollapsibleContent>
      </li>
    </Collapsible>
  );
}

function EnvironmentCard({ svc, gitManaged, canEdit }: { svc: ServiceT; gitManaged: boolean; canEdit: boolean }) {
  const qc = useQueryClient();
  // A database's env is generated at creation: shown, never edited. A git
  // project's env is its compose file's.
  const isDb = isDatabase(svc.kind);
  const readOnly = isDb || gitManaged || !canEdit;
  const project = useQuery({ queryKey: ["project", svc.projectId], queryFn: () => api.project(svc.projectId), enabled: !isDb });
  const siblings = useQuery({ queryKey: ["services", svc.projectId], queryFn: () => api.services(svc.projectId), enabled: !isDb });
  const databases = siblings.data?.flatMap((s) => (isDatabase(s.kind) ? [{ name: s.name, kind: s.kind }] : []));
  const [rows, setRows] = useState<EnvRow[]>(() => envRows(svc.env, svc.secrets));
  const dirty = !sameEnv(rows, svc.env, svc.secrets);
  const formId = useId();
  const save = useMutation({
    meta: { error: "Couldn't save the environment" },
    mutationFn: () => api.updateService(svc.id, { env: envMap(rows), secrets: envSecrets(rows) }),
    onSuccess: (updated) => {
      qc.setQueryData(["service", svc.id], updated);
      setRows(envRows(updated.env, updated.secrets));
      toast.success("Environment saved", { description: "Deploy to apply." });
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <Section
      plain
      description={
        isDb ? (
          "Generated at creation and shared with the apps that connect, so it can't be changed."
        ) : (
          <>
            Use the project&apos;s shared entries with <Mono>{"{{ project.NAME }}"}</Mono>, its databases with{" "}
            <Mono>{"{{ db.NAME.URL }}"}</Mono> and password manager secrets with{" "}
            <Mono>{"{{ pass://Vault/Item/field }}"}</Mono> (Integrations › Password managers). Changes apply on the next
            deploy.
          </>
        )
      }
    >
      <form id={formId} onSubmit={onSubmit} className="space-y-4">
        <EnvEditor
          showLabel={false}
          rows={rows}
          onChange={setRows}
          vars={Object.keys(project.data?.env ?? {}).sort()}
          secretVars={project.data?.secrets}
          databases={databases}
          passwordManagers
          readOnly={readOnly}
        />
      </form>
      {!readOnly && <SaveBar form={formId} dirty={dirty} saving={save.isPending} onReset={() => setRows(envRows(svc.env, svc.secrets))} />}
    </Section>
  );
}

function BackupsCard({
  serviceId,
  name,
  kind,
  canBackUp,
}: {
  serviceId: string;
  name: string;
  kind: ServiceT["kind"];
  canBackUp: boolean;
}) {
  const qc = useQueryClient();
  const targets = useQuery({ queryKey: ["storage"], queryFn: api.backupTargets });
  // A PostgreSQL instance may hold several databases; a backup picks one. Stopped: no list, the main one.
  const databases = useQuery({
    queryKey: ["data", serviceId, "databases"],
    queryFn: () => api.pgDatabases(serviceId),
    enabled: kind === "postgres",
    retry: false,
    meta: { error: false },
  });
  const backups = useQuery({
    queryKey: ["backups", serviceId],
    queryFn: () => api.serviceBackups(serviceId),
    refetchInterval: (q) => (q.state.data?.some((b) => b.status === "running") ? 1_000 : 15_000),
  });
  const restores = useQuery({
    queryKey: ["restores", serviceId],
    queryFn: () => api.restores(serviceId),
    refetchInterval: (q) => (q.state.data?.some((r) => r.status === "running") ? 1_000 : 15_000),
  });
  const backup = async (targetId: string, database: string) => {
    const b = await api.backup(serviceId, targetId, database);
    qc.invalidateQueries({ queryKey: ["backups"] });
    return b;
  };
  const lastRestore = restores.data?.[0];
  const busy = backups.data?.some((b) => b.status === "running") || lastRestore?.status === "running";

  return (
    <Section
      plain
      actions={
        canBackUp && <BackupNowDialog what={name} targets={targets.data ?? []} disabled={busy} onBackup={backup} databases={databases.data} />
      }
    >
      {lastRestore && (
        <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
          Last restore <StateBadge state={lastRestore.status} /> {timeAgo(lastRestore.createdAt)}
          {lastRestore.error && <span className="truncate text-destructive">{lastRestore.error}</span>}
        </div>
      )}
      <Schedules serviceId={serviceId} targets={targets.data ?? []} databases={databases.data} />
      <div className="space-y-2">
        <h3 className="text-sm font-medium">History</h3>
        <BackupList backups={backups.data ?? []} targets={targets.data ?? []} />
      </div>
    </Section>
  );
}

/** The CI step that deploys the tag it just pushed. */
function DeployFromCIDialog({ svc }: { svc: ServiceT }) {
  const repo = imageRepo(svc.image);
  const step = [
    "- name: Deploy",
    "  run: >",
    "    docker run --rm -e KIPITINY_TOKEN=${{ secrets.KIPITINY_TOKEN }}",
    "    ghcr.io/mathoyer/kipitiny deploy",
    `    --url ${window.location.origin} --service ${svc.id}`,
    "    --tag sha-${{ github.sha }} --commit ${{ github.sha }}",
  ].join("\n");
  return (
    <Dialog>
      <DialogTrigger asChild>
        <Button variant="outline" size="sm">
          <Workflow data-icon="inline-start" />
          Deploy from CI
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>Deploy from CI</DialogTitle>
          <DialogDescription>
            Build and push <Mono>{repo}</Mono> in CI, then deploy the tag it pushed with a <Mono>deploy</Mono> token (Access › API
            tokens & MCP). The service keeps that tag; the step fails if the deployment does.
          </DialogDescription>
        </DialogHeader>
        <div className="relative">
          <pre className={cn(terminal, "pr-10")}>{step}</pre>
          <div className="absolute top-1.5 right-1.5 text-neutral-200">
            <CopyButton value={step} label="Copy step" />
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

/** The image without its tag or digest. */
function imageRepo(image: string) {
  const name = image.split("@")[0];
  const colon = name.lastIndexOf(":");
  return colon > name.lastIndexOf("/") ? name.slice(0, colon) : name;
}

/** The image without its pinned digest: name:tag stays readable. */
function shortImage(image: string) {
  return image.split("@")[0];
}

/** Who started a deployment, for display. */
function triggeredBy(t?: string) {
  if (!t) return "";
  const [kind, name] = [t.slice(0, t.indexOf(":")), t.slice(t.indexOf(":") + 1)];
  if (kind === "token") return `token ${name}`;
  return name;
}

function ConnectionCard({ serviceId, host, kind }: { serviceId: string; host: string; kind: DatabaseKind }) {
  const [conn, setConn] = useState<Connection | null>(null);
  const reveal = useMutation({ meta: { error: "Couldn't show the connection details" }, mutationFn: () => api.connection(serviceId), onSuccess: setConn });
  return (
    <Section
      title="Connection"
      description={
        <>
          Reachable only inside the project at <Mono>{`${host}:${databasePorts[kind]}`}</Mono>. Apps use it from their
          Environment (Secrets → Connect database), e.g.{" "}
          <Mono>{`${databaseUrlKey(kind)}={{ db.${host}.URL }}`}</Mono>.
        </>
      }
      actions={
        <Button variant="outline" size="sm" onClick={() => (conn ? setConn(null) : reveal.mutate())} disabled={reveal.isPending}>
          {conn ? "Hide" : "Reveal credentials"}
        </Button>
      }
    >
      {conn && (
        <SecretList
          rows={[
            ["Host", `${conn.host}:${conn.port}`],
            ...(conn.database ? [["Database", conn.database] as [string, string]] : []),
            ["User", conn.user],
            ["Password", conn.password],
            ["URL", conn.url],
          ]}
        />
      )}
    </Section>
  );
}

function Deployments({
  svc,
  deployments,
  focus,
  onFocus,
  busy,
}: {
  svc: ServiceT;
  deployments: Deployment[];
  /** Opened when it changes, e.g. a deploy just started. */
  focus: string | null;
  onFocus: (id: string) => void;
  busy: boolean;
}) {
  const qc = useQueryClient();
  const [open, setOpen] = useState<Set<string>>(() => {
    const live = deployments[0]?.status === "running" ? deployments[0].id : null;
    return new Set([focus, live].filter((x) => x !== null));
  });
  useEffect(() => {
    if (focus) setOpen((s) => new Set(s).add(focus));
  }, [focus]);
  const toggle = (id: string, on: boolean) =>
    setOpen((s) => {
      const next = new Set(s);
      if (on) next.add(id);
      else next.delete(id);
      return next;
    });

  const rollback = useMutation({
    meta: { error: "Couldn't roll back" },
    mutationFn: (deploymentId: string) => api.rollback(svc.id, deploymentId),
    onSuccess: (d) => {
      onFocus(d.id);
      qc.invalidateQueries({ queryKey: ["deployments", svc.id] });
    },
  });

  const ci = svc.kind === "app" && <DeployFromCIDialog svc={svc} />;
  if (deployments.length === 0)
    return <EmptyState icon={Rocket} title="No deployments yet" description="Deploy the service to see its builds and logs here." action={ci} />;

  return (
    <div className="space-y-6">
      {ci && <div className="flex justify-end">{ci}</div>}
      {byDay(deployments).map(([day, rows]) => (
        <section key={day} className="space-y-2">
          <h3 className="text-xs font-medium tracking-wide text-muted-foreground uppercase">{day}</h3>
          <ul className="divide-y overflow-hidden rounded-xl border">
            {rows.map((d) => (
              <DeploymentRow
                key={d.id}
                deployment={d}
                current={d.id === svc.currentDeploymentId}
                open={open.has(d.id)}
                onOpenChange={(on) => toggle(d.id, on)}
                onRollback={
                  svc.kind === "app" && d.status === "succeeded" && d.id !== svc.currentDeploymentId && !busy
                    ? () => rollback.mutate(d.id)
                    : undefined
                }
              />
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

const statusIcon = {
  succeeded: <CircleCheck className="size-4.5 text-emerald-600 dark:text-emerald-400" />,
  failed: <CircleX className="size-4.5 text-destructive" />,
  skipped: <CircleSlash className="size-4.5 text-pink-500" />,
  running: <Spinner className="size-4.5 text-sky-500" />,
} satisfies Record<Deployment["status"], ReactNode>;

function DeploymentRow({
  deployment: d,
  current,
  open,
  onOpenChange,
  onRollback,
}: {
  deployment: Deployment;
  current: boolean;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onRollback?: () => void;
}) {
  const end = d.finishedAt ? new Date(d.finishedAt).getTime() : Date.now();
  const took = formatDuration(Math.max(0, end - new Date(d.createdAt).getTime()));
  return (
    <Collapsible asChild open={open} onOpenChange={onOpenChange}>
      <li className={cn(open && "bg-muted/30")}>
        <div className="flex items-center gap-2 pr-3">
          <CollapsibleTrigger className="group flex min-w-0 flex-1 items-center gap-3 py-3 pl-3 text-left outline-none focus-visible:bg-muted/50">
            <ChevronRight className="size-4 shrink-0 text-muted-foreground transition-transform group-data-[state=open]:rotate-90" />
            <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted">{statusIcon[d.status]}</span>
            <div className="min-w-0 flex-1">
              <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
                {d.gitCommit ? (
                  <span className="inline-flex items-center gap-1.5 font-mono">
                    <GitCommitHorizontal className="size-4 text-muted-foreground" />
                    {d.gitCommit.slice(0, 7)}
                  </span>
                ) : (
                  <span className="truncate font-mono" title={d.image}>
                    {shortImage(d.image)}
                  </span>
                )}
                {current && <Tag className="bg-emerald-500/10 text-emerald-700 dark:text-emerald-400">current</Tag>}
              </p>
              <p className="truncate text-xs text-muted-foreground">
                {new Date(d.createdAt).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })} ·{" "}
                {d.status === "running" ? `running for ${took}` : took}
                {d.triggeredBy && <> · by {triggeredBy(d.triggeredBy)}</>}
                {d.error && <span className={d.status === "skipped" ? "text-pink-600 dark:text-pink-400" : "text-destructive"}> · {d.error}</span>}
              </p>
            </div>
            <span className="text-xs whitespace-nowrap text-muted-foreground max-sm:hidden">{timeAgo(d.createdAt)}</span>
          </CollapsibleTrigger>
          {onRollback && (
            <ConfirmDialog
              trigger={
                <Button variant="outline" size="sm">
                  <RefreshCw data-icon="inline-start" />
                  Roll back
                </Button>
              }
              title="Roll back?"
              description={`Redeploys ${d.image}. Current settings are kept; the next regular deploy uses the image in Settings again.`}
              confirmLabel="Roll back"
              destructive={false}
              onConfirm={onRollback}
            />
          )}
        </div>
        <CollapsibleContent>
          <DeploymentLog deployment={d} />
        </CollapsibleContent>
      </li>
    </Collapsible>
  );
}

const terminal = "overflow-auto rounded-lg bg-neutral-950 p-3 font-mono text-xs leading-relaxed text-neutral-200 dark:ring-1 dark:ring-foreground/10";

function DeploymentLog({ deployment }: { deployment: Deployment }) {
  const log = useQuery({
    queryKey: ["deployment-log", deployment.id],
    queryFn: () => api.deploymentLog(deployment.id),
    refetchInterval: deployment.status === "running" ? 1_000 : false,
  });
  return (
    <div className="space-y-2 px-3 pb-3">
      {deployment.image && deployment.gitCommit && (
        <p className="truncate font-mono text-xs text-muted-foreground">{deployment.image}</p>
      )}
      {deployment.error && <p className="text-sm text-destructive">{deployment.error}</p>}
      <DeploymentLogView text={log.data} pending={log.isPending} className="max-h-96" />
    </div>
  );
}

// xterm.js is big: load it with the first terminal.
const TerminalView = lazy(() => import("@/components/terminal").then((m) => ({ default: m.TerminalView })));

const shells = [
  { value: "auto", label: "bash, or sh" },
  { value: "sh", label: "sh" },
  { value: "bash", label: "bash" },
];

/** A shell in one of the service's running containers. */
function ServiceTerminal({ svc }: { svc: ServiceT }) {
  const running = svc.containers.filter((c) => !c.retired && c.state === "running");
  const [picked, setPicked] = useState("");
  const [shell, setShell] = useState("auto");
  // Each connect mounts a new session; an ended one stays on screen.
  const [session, setSession] = useState<{ n: number; path: string } | null>(null);
  const [live, setLive] = useState(false);
  const container = running.some((c) => c.name === picked) ? picked : (running[0]?.name ?? "");

  if (running.length === 0 && !session) return <Empty>No running container.</Empty>;

  return (
    <Section
      plain
      actions={
        <>
          {running.length > 1 && (
            <Select value={container} onValueChange={setPicked} disabled={live}>
              <SelectTrigger size="sm" aria-label="Replica">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {running.map((c) => (
                  <SelectItem key={c.id} value={c.name}>
                    {c.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
          <Select value={shell} onValueChange={setShell} disabled={live}>
            <SelectTrigger size="sm" aria-label="Shell">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {shells.map((s) => (
                <SelectItem key={s.value} value={s.value}>
                  {s.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {live ? (
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                setSession(null);
                setLive(false);
              }}
            >
              Disconnect
            </Button>
          ) : (
            <Button
              size="sm"
              disabled={!container}
              onClick={() => {
                setSession((s) => ({ n: (s?.n ?? 0) + 1, path: api.terminalUrl(svc.id, container, shell) }));
                setLive(true);
              }}
            >
              <SquareTerminal data-icon="inline-start" />
              {session ? "Reconnect" : "Connect"}
            </Button>
          )}
        </>
      }
    >
      {!session ? (
        <p className="text-sm text-muted-foreground">
          Opens a shell in {running.length > 1 ? "the chosen replica" : container}. Sessions are recorded in the audit log.
        </p>
      ) : (
        <Suspense fallback={<Loading />}>
          <TerminalView
            key={session.n}
            path={session.path}
            onClose={() => setLive(false)}
            className="h-[calc(100svh-24rem)] min-h-80"
          />
        </Suspense>
      )}
    </Section>
  );
}
