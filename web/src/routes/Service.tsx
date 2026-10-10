import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  ChevronRight,
  CircleCheck,
  CircleX,
  ExternalLink,
  GitCommitHorizontal,
  Globe,
  Layers,
  Play,
  Plus,
  RefreshCw,
  RotateCcw,
  Rocket,
  Square,
  SquareTerminal,
  Trash2,
} from "lucide-react";
import { lazy, Suspense, useEffect, useId, useRef, useState, type FormEvent, type ReactNode } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { toast } from "sonner";
import { portLabel } from "@/components/reach";
import { ServiceIcon, serviceLogos } from "@/components/service-icon";
import { FloatingSelect } from "@/components/ui/floating-select";
import { CheckboxField, CopyButton, DangerZone, Empty, EmptyState, ErrorText, Mono, SecretList, Section, StatCard, StateBadge, Tag, Loading } from "@/components/common";
import { BasicAuthUsers, type BasicAuthRow } from "@/components/basic-auth-users";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DataTab } from "@/components/data/data-tab";
import { DomainField } from "@/components/domain-field";
import { EnvEditor } from "@/components/env-editor";
import { PageBody, PageHeader } from "@/components/page-header";
import { DeploymentLogView, LiveLogs } from "@/components/logs";
import { RenameCard } from "@/components/rename-card";
import { SaveBar } from "@/components/save-bar";
import { UptimeSection } from "@/components/uptime";
import { ServiceUsageSection, UsageGrid, useServiceStats } from "@/components/usage";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { Card } from "@/components/ui/card";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingTextarea } from "@/components/ui/floating-textarea";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useTab } from "@/hooks/use-tab";
import { byDay, envMap, envRows, envSecrets, formatBytes, formatCpu, formatDuration, sameEnv, serviceState, timeAgo, type EnvRow } from "@/lib/format";
import { cn } from "@/lib/utils";
import { api, canBackup, databasePorts, httpRouted, isDatabase, type Connection, type Container as ContainerT, type DatabaseKind, type Deployment, type Middlewares, type MiddlewaresInput, type PublishedPort, type Service as ServiceT, type Volume } from "../api";
import { BackupList } from "./BackupList";
import { Schedules } from "./Schedules";
import { BackupNowDialog } from "@/components/backup-now-dialog";
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
  const [tab, setTab] = useTab(["overview", "deployments", "logs", "terminal", "data", "environment", "backups", "settings"], "overview");
  const git = useProjectGit(service.data?.projectId ?? "");
  const gitManaged = !!git.data;

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
  const rename = useMutation({
    meta: { error: "Couldn't rename the service" },
    mutationFn: (name: string) => api.renameService(id, name),
    onSuccess: (svc) => {
      qc.setQueryData(["service", id], svc);
      qc.invalidateQueries({ queryKey: ["services", svc.projectId] });
    },
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
  const state = deploying ? "deploying" : svc.stopped ? "stopped" : serviceState(active);
  const allStopped = active.length > 0 && active.every((c) => c.state !== "running");
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
            {svc.containers.length > 0 && (
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
            <Button size="sm" loading={deploying} disabled={busy} onClick={() => deploy.mutate()}>
              <Rocket data-icon="inline-start" />
              {deploying ? "Deploying" : "Deploy"}
            </Button>
          </>
        }
      />
      <PageBody>
        {svc.dns && svc.dns.state !== "synced" && (
          <p role="alert" className="text-sm text-destructive">
            DNS {svc.dns.state}: {svc.dns.message}
          </p>
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
              <p className="truncate font-mono text-sm">
                {svc.domain}
                {svc.publishedPorts[0] && `:${svc.publishedPorts[0].hostPort}`}
              </p>
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
            <TabsTrigger value="terminal">Terminal</TabsTrigger>
            {isDb && <TabsTrigger value="data">Data</TabsTrigger>}
            <TabsTrigger value="environment">Environment</TabsTrigger>
            {canBackup(svc) && <TabsTrigger value="backups">Backups</TabsTrigger>}
            <TabsTrigger value="settings">Settings</TabsTrigger>
          </TabsList>
          <TabsContent value="overview" className="space-y-6">
            {svc.containers.length > 0 && <ServiceUsageSection serviceId={svc.id} />}
            {svc.kind === "app" && <UptimeSection svc={svc} />}
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
            {isDatabase(svc.kind) && <ConnectionCard serviceId={svc.id} host={svc.name} kind={svc.kind} />}
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
          <TabsContent value="terminal">
            <ServiceTerminal svc={svc} />
          </TabsContent>
          {isDb && (
            <TabsContent value="data">
              <DataTab svc={svc} />
            </TabsContent>
          )}
          <TabsContent value="environment" className="flex flex-col gap-6">
            {gitManaged && <GitManagedNote path={git.data!.path} />}
            <fieldset disabled={gitManaged} className="contents">
              <EnvironmentCard key={svc.id} svc={svc} />
            </fieldset>
          </TabsContent>
          {canBackup(svc) && (
            <TabsContent value="backups">
              <BackupsCard serviceId={svc.id} kind={svc.kind} name={project.data ? `${project.data.name}/${svc.name}` : svc.name} />
            </TabsContent>
          )}
          <TabsContent value="settings" className="flex flex-col gap-6">
            {gitManaged && <GitManagedNote path={git.data!.path} />}
            <RenameCard
              key={`rename-${svc.name}`}
              name={svc.name}
              description={
                isDb
                  ? "Nothing restarts. The apps using it get their references updated and are redeployed."
                  : "Nothing restarts. Until its next deploy, other services can still reach it by its old name."
              }
              disabled={gitManaged}
              pending={rename.isPending}
              onRename={(name) => rename.mutate(name)}
            />
            <fieldset disabled={gitManaged} className="contents">
              <Settings svc={svc} />
              {/* Keys reset each card's form when another service opens; siblings need distinct ones. */}
              {svc.kind === "app" && <PortsCard key={`ports-${svc.id}`} svc={svc} />}
              {svc.kind === "app" && <HostAccessCard key={`host-${svc.id}`} svc={svc} />}
              {svc.kind === "app" && <VolumesCard key={`volumes-${svc.id}`} svc={svc} />}
              {svc.kind === "app" && httpRouted(svc) && <AccessCard key={`access-${svc.id}`} svc={svc} />}
            </fieldset>
            {/* Not in the compose file, so git projects set them here too. */}
            {!svc.hostNetwork && <NetworksCard key={`networks-${svc.id}`} svc={svc} projectName={project.data?.name ?? ""} />}
            {svc.kind === "app" && <DeployFromCICard svc={svc} />}
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

/** The settings form's fields, as saved. */
const settingsForm = (s: ServiceT) => ({
  image: s.image,
  icon: s.icon || AUTO_ICON,
  domain: s.domain,
  port: s.port ? String(s.port) : "",
  replicas: String(s.replicas),
  memory: s.memoryMb ? String(s.memoryMb) : "",
  cpus: s.cpus ? String(s.cpus) : "",
  healthPath: s.healthPath,
  preDeploy: s.preDeploy,
  stopGrace: s.stopGraceSeconds ? String(s.stopGraceSeconds) : "",
});

/** Radix selects can't hold "": this stands for an empty icon. */
const AUTO_ICON = "auto";

/** Automatic (from the image) or a known logo; an unknown saved icon stays listed. */
function iconOptions(svc: ServiceT, image: string) {
  const option = (value: string, icon: string | undefined, label: string) => ({
    value,
    label: (
      <span className="flex items-center gap-2">
        <ServiceIcon service={{ icon, kind: svc.kind, image: icon ? undefined : image }} className="size-4 shrink-0" />
        {label}
      </span>
    ),
  });
  const names = Object.keys(serviceLogos);
  if (svc.icon && !names.includes(svc.icon)) names.push(svc.icon);
  return [
    option(AUTO_ICON, undefined, "Automatic, from the image"),
    ...names.map((name) => option(name, name, serviceLogos[name]?.label ?? name)),
  ];
}

function Settings({ svc }: { svc: ServiceT }) {
  const qc = useQueryClient();
  const isDb = isDatabase(svc.kind);
  const [form, setForm] = useState(() => settingsForm(svc));
  const dirty = JSON.stringify(form) !== JSON.stringify(settingsForm(svc));
  const formId = useId();
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });

  const save = useMutation({
    meta: { error: "Couldn't save the settings" },
    mutationFn: () =>
      api.updateService(
        svc.id,
        isDb
          ? { image: form.image.trim(), memoryMb: Number(form.memory) || 0, cpus: Number(form.cpus) || 0 }
          : {
              image: form.image.trim(),
              icon: form.icon === AUTO_ICON ? "" : form.icon,
              domain: form.domain.trim(),
              port: Number(form.port) || 0,
              replicas: Number(form.replicas) || 1,
              memoryMb: Number(form.memory) || 0,
              cpus: Number(form.cpus) || 0,
              healthPath: form.healthPath.trim(),
              preDeploy: form.preDeploy.trim(),
              stopGraceSeconds: Number(form.stopGrace) || 0,
            },
      ),
    onSuccess: (updated) => {
      qc.setQueryData(["service", svc.id], updated);
      setForm(settingsForm(updated));
      toast.success("Settings saved", {
        description: isDb ? "Deploy to apply." : "Replica count applies now; deploy to apply other changes.",
      });
    },
  });

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <Section plain>
      <form id={formId} onSubmit={onSubmit} className="grid gap-4 sm:grid-cols-2">
        <FloatingInput
          label="Image"
          required
          value={form.image}
          onChange={set("image")}
          className="sm:col-span-2"
          description={svc.kind === "postgres" ? "Minor upgrades only; major versions need a dump and restore." : undefined}
        />
        {!isDb && (
          <>
            <FloatingSelect
              label="Icon"
              value={form.icon}
              onValueChange={(icon) => setForm({ ...form, icon })}
              options={iconOptions(svc, form.image)}
              className="sm:col-span-2"
            />
            <DomainField value={form.domain} onChange={(domain) => setForm({ ...form, domain })} className="sm:col-span-2" />
            <FloatingInput
              label="Port"
              type="number"
              min={0}
              max={65535}
              value={form.port}
              onChange={set("port")}
              description={
                svc.publishedPorts.length > 0 ? "HTTP port Traefik routes the domain to. Empty: the domain is only for DNS." : undefined
              }
            />
            <FloatingInput
              label="Replicas"
              type="number"
              min={1}
              max={10}
              value={form.replicas}
              onChange={set("replicas")}
              disabled={svc.publishedPorts.length > 0 || svc.hostNetwork}
              description={
                svc.publishedPorts.length > 0
                  ? "One: two containers can't publish the same port."
                  : svc.hostNetwork
                    ? "One: the app runs in the host's network."
                    : undefined
              }
            />
          </>
        )}
        <FloatingInput
          label="Memory (MB)"
          type="number"
          min={0}
          step={64}
          value={form.memory}
          onChange={set("memory")}
          description={isDb ? undefined : "Empty for no limit."}
        />
        <FloatingInput
          label="CPUs"
          type="number"
          min={0}
          step={0.25}
          value={form.cpus}
          onChange={set("cpus")}
          placeholder="0.5"
          description="Cores, e.g. 0.5. Empty for no limit."
        />
        {!isDb && (
          <>
            <FloatingInput
              label="Health path"
              value={form.healthPath}
              onChange={set("healthPath")}
              placeholder="/healthz"
              className="sm:col-span-2"
              description="New replicas get traffic once this answers < 400. Empty: port accepts connections."
            />
            <FloatingInput
              label="Pre-deploy command"
              value={form.preDeploy}
              onChange={set("preDeploy")}
              placeholder="npm run migrate"
              inputClassName="font-mono"
              className="sm:col-span-2"
              description="Runs once (sh -c) before replicas start, e.g. migrations."
            />
            <FloatingInput
              label="Stop grace period (s)"
              type="number"
              min={0}
              max={600}
              value={form.stopGrace}
              onChange={set("stopGrace")}
              placeholder="10"
              description="Time to exit after SIGTERM before being killed, e.g. to save a game world. Empty: 10 s."
            />
          </>
        )}
      </form>
      <SaveBar form={formId} dirty={dirty} saving={save.isPending} onReset={() => setForm(settingsForm(svc))} />
    </Section>
  );
}

type PortRow = { hostPort: string; containerPort: string; protocol: PublishedPort["protocol"] };

const portRows = (ports: PublishedPort[]): PortRow[] =>
  ports.map((p) => ({ hostPort: String(p.hostPort), containerPort: String(p.containerPort), protocol: p.protocol }));

/** Host ports bound straight to an app's container, for traffic that isn't HTTP. */
function PortsCard({ svc }: { svc: ServiceT }) {
  const qc = useQueryClient();
  const [rows, setRows] = useState<PortRow[]>(() => portRows(svc.publishedPorts));
  const dirty = JSON.stringify(rows) !== JSON.stringify(portRows(svc.publishedPorts));
  const formId = useId();
  const save = useMutation({
    meta: { error: "Couldn't save the ports" },
    mutationFn: () =>
      api.updateService(svc.id, {
        publishedPorts: rows.map((r) => ({
          hostPort: Number(r.hostPort) || 0,
          containerPort: Number(r.containerPort) || 0,
          protocol: r.protocol,
        })),
        // An app publishing ports runs one replica.
        ...(rows.length > 0 && svc.replicas !== 1 ? { replicas: 1 } : {}),
      }),
    onSuccess: (updated) => {
      qc.setQueryData(["service", svc.id], updated);
      setRows(portRows(updated.publishedPorts));
      toast.success("Ports saved", { description: "Deploy to apply." });
    },
  });
  const set = (i: number, field: keyof PortRow) => (value: string) =>
    setRows(rows.map((r, j) => (j === i ? { ...r, [field]: value } : r)));
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <Section
      title="Published ports"
      description="Ports of the server bound straight to the container, for traffic that isn't HTTP (a game server, MQTT…). The app then runs one replica, and deploys stop the old container before starting the new one: a short downtime. Open the ports in the server's firewall; a Cloudflare tunnel doesn't carry them."
      actions={
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={() => setRows([...rows, { hostPort: "", containerPort: "", protocol: "tcp" }])}
        >
          <Plus data-icon="inline-start" />
          Add port
        </Button>
      }
    >
      {/* Always rendered: the save bar submits it, even to remove the last port. */}
      <form id={formId} onSubmit={onSubmit} className="space-y-3">
        {rows.length === 0 && <Empty>No published ports: the app is only reachable through its domain, if any.</Empty>}
        {rows.map((r, i) => (
          <div key={i} className="flex items-start gap-2">
            <FloatingInput
              label="Host port"
              required
              type="number"
              min={1}
              max={65535}
              value={r.hostPort}
              onChange={(e) => set(i, "hostPort")(e.target.value)}
              placeholder="25565"
              className="min-w-0 flex-1"
              inputClassName="font-mono"
            />
            <FloatingInput
              label="Container port"
              type="number"
              min={1}
              max={65535}
              value={r.containerPort}
              onChange={(e) => set(i, "containerPort")(e.target.value)}
              placeholder={r.hostPort || "same"}
              className="min-w-0 flex-1"
              inputClassName="font-mono"
            />
            <Select value={r.protocol} onValueChange={set(i, "protocol")}>
              <SelectTrigger aria-label="Protocol" className="w-24 shrink-0 rounded-xl data-[size=default]:h-14">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="tcp">TCP</SelectItem>
                <SelectItem value="udp">UDP</SelectItem>
              </SelectContent>
            </Select>
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              title="Remove"
              aria-label={`Remove port ${r.hostPort}`}
              className="mt-3.5 text-muted-foreground hover:text-destructive"
              onClick={() => setRows(rows.filter((_, j) => j !== i))}
            >
              <Trash2 />
            </Button>
          </div>
        ))}
      </form>
      <SaveBar form={formId} dirty={dirty} saving={save.isPending} onReset={() => setRows(portRows(svc.publishedPorts))} label="Save ports" />
    </Section>
  );
}

/** Host network and Docker socket, for agents that report on or manage the server (Beszel, node-exporter…). */
function HostAccessCard({ svc }: { svc: ServiceT }) {
  const qc = useQueryClient();
  const initial = { hostNetwork: svc.hostNetwork, dockerSocket: svc.dockerSocket };
  const [form, setForm] = useState(initial);
  const dirty = form.hostNetwork !== initial.hostNetwork || form.dockerSocket !== initial.dockerSocket;
  const formId = useId();
  const blocked = form.hostNetwork && (!!svc.domain || svc.publishedPorts.length > 0);
  const save = useMutation({
    meta: { error: "Couldn't save host access" },
    mutationFn: () =>
      api.updateService(svc.id, {
        ...form,
        // An app in the host network runs one replica.
        ...(form.hostNetwork && svc.replicas !== 1 ? { replicas: 1 } : {}),
      }),
    onSuccess: (updated) => {
      qc.setQueryData(["service", svc.id], updated);
      setForm({ hostNetwork: updated.hostNetwork, dockerSocket: updated.dockerSocket });
      toast.success("Host access saved", { description: "Deploy to apply." });
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <Section
      title="Host access"
      description="For agents that report on or manage the server, such as a Beszel agent. Only give it to images you trust: the Docker socket, even read-only, gives control of the server."
    >
      <form id={formId} onSubmit={onSubmit} className="space-y-4">
        <CheckboxField
          label="Host network"
          description="The app shares the server's network: it sees the host's interfaces and listens on its ports directly. No domain, published ports or project network (other services can't reach it by name), one replica, and deploys stop the old container first."
          checked={form.hostNetwork}
          onCheckedChange={(v) => setForm({ ...form, hostNetwork: v })}
        />
        {blocked && <p className="text-sm text-destructive">Remove the domain and published ports first.</p>}
        <FloatingSelect
          label="Docker socket"
          value={form.dockerSocket || "none"}
          onValueChange={(v) => setForm({ ...form, dockerSocket: v === "none" ? "" : (v as "ro" | "rw") })}
          options={[
            { value: "none", label: "Not mounted" },
            { value: "ro", label: "Read-only (container stats, e.g. Beszel)" },
            { value: "rw", label: "Read-write (manages containers)" },
          ]}
          description={<>Mounted at <Mono>/var/run/docker.sock</Mono>.</>}
        />
      </form>
      <SaveBar form={formId} dirty={dirty && !blocked} saving={save.isPending} onReset={() => setForm(initial)} label="Save host access" />
    </Section>
  );
}

/** Networks created by hand the service joins, to reach services of other projects. */
function NetworksCard({ svc, projectName }: { svc: ServiceT; projectName: string }) {
  const qc = useQueryClient();
  const networks = useQuery({ queryKey: ["networks"], queryFn: api.networks });
  const [selected, setSelected] = useState(svc.networks);
  const dirty = [...selected].sort().join() !== [...svc.networks].sort().join();
  const formId = useId();
  const available = networks.data?.filter((n) => n.serverId === svc.serverId) ?? [];
  const save = useMutation({
    meta: { error: "Couldn't save the networks" },
    mutationFn: () => api.setServiceNetworks(svc.id, selected),
    onSuccess: (updated) => {
      qc.setQueryData(["service", svc.id], updated);
      setSelected(updated.networks);
      toast.success("Networks saved", { description: "Applied to the running containers; nothing restarted." });
    },
    onSettled: () => qc.invalidateQueries({ queryKey: ["networks"] }),
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };
  const toggle = (id: string, on: boolean) => setSelected(on ? [...selected, id] : selected.filter((x) => x !== id));

  return (
    <Section
      title="Networks"
      description={
        <>
          Its project network is automatic: the project's services reach it as <Mono>{svc.name}</Mono>. On the networks below, services of
          other projects reach it as <Mono>{`${projectName}-${svc.name}`}</Mono>.
        </>
      }
    >
      <ErrorText error={networks.error} />
      <form id={formId} onSubmit={onSubmit} className="space-y-4">
        {networks.isPending ? (
          <Loading />
        ) : available.length === 0 ? (
          <Empty>
            No networks created by hand on this server yet: create one in{" "}
            <Link to="/networks" className="underline">
              Infrastructure › Networks
            </Link>
            .
          </Empty>
        ) : (
          available.map((n) => {
            const others = n.services.filter((m) => m.id !== svc.id);
            return (
              <CheckboxField
                key={n.id}
                label={n.name}
                description={others.length ? `With ${others.map((m) => m.alias).join(", ")}` : "No other services on it yet."}
                checked={selected.includes(n.id)}
                onCheckedChange={(v) => toggle(n.id, v)}
              />
            );
          })
        )}
      </form>
      <SaveBar form={formId} dirty={dirty} saving={save.isPending} onReset={() => setSelected(svc.networks)} label="Save networks" />
    </Section>
  );
}

const volumeNameRe = /^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$/;

/** Named volumes of an app: kept across deploys, shared by its replicas. */
function VolumesCard({ svc }: { svc: ServiceT }) {
  const qc = useQueryClient();
  const [rows, setRows] = useState<Volume[]>(svc.volumes);
  const [preBackup, setPreBackup] = useState(svc.preBackup);
  const dirty = JSON.stringify(rows) !== JSON.stringify(svc.volumes) || preBackup !== svc.preBackup;
  const formId = useId();
  const save = useMutation({
    meta: { error: "Couldn't save the volumes" },
    mutationFn: () =>
      api.updateService(svc.id, {
        volumes: rows.map((v) => ({ name: v.name.trim(), path: v.path.trim() })),
        preBackup: rows.length > 0 ? preBackup.trim() : "",
      }),
    onSuccess: (updated) => {
      qc.setQueryData(["service", svc.id], updated);
      setRows(updated.volumes);
      setPreBackup(updated.preBackup);
      toast.success("Volumes saved", { description: "Deploy to apply." });
    },
  });
  const reset = () => {
    setRows(svc.volumes);
    setPreBackup(svc.preBackup);
  };
  const set = (i: number, field: keyof Volume) => (e: { target: { value: string } }) =>
    setRows(rows.map((v, j) => (j === i ? { ...v, [field]: e.target.value } : v)));
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <Section
      title="Volumes"
      description="Data that outlives deploys, e.g. uploads. Every replica mounts the same volume. A volume removed here keeps its data until the service is deleted."
      actions={
        <Button type="button" size="sm" variant="outline" onClick={() => setRows([...rows, { name: "", path: "" }])}>
          <Plus data-icon="inline-start" />
          Add volume
        </Button>
      }
    >
      {/* Always rendered: the save bar submits it, even to remove the last volume. */}
      <form id={formId} onSubmit={onSubmit} className="space-y-3">
        {rows.length === 0 && <Empty>No volumes: files written in the containers are lost on every deploy.</Empty>}
        {rows.map((v, i) => {
          const name = v.name.trim();
          const badName = name !== "" && !volumeNameRe.test(name);
          const isNew = name !== "" && !svc.volumes.some((o) => o.name === name);
          return (
            <div key={i} className="flex items-start gap-2">
              <FloatingInput
                label="Name"
                required
                value={v.name}
                onChange={set(i, "name")}
                placeholder="uploads"
                className="w-40 shrink-0 sm:w-56"
                inputClassName="font-mono"
                aria-invalid={badName}
                description={badName ? "Lowercase letters, digits and dashes." : isNew ? "New volume, created empty." : undefined}
              />
              <FloatingInput
                label="Mount path"
                required
                value={v.path}
                onChange={set(i, "path")}
                placeholder="/app/uploads"
                className="min-w-0 flex-1"
                inputClassName="font-mono"
              />
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                title="Remove"
                aria-label={`Remove ${v.name || "volume"}`}
                className="mt-3.5 text-muted-foreground hover:text-destructive"
                onClick={() => setRows(rows.filter((_, j) => j !== i))}
              >
                <Trash2 />
              </Button>
            </div>
          );
        })}
        {rows.length > 0 && (
          <FloatingInput
            label="Pre-backup command"
            value={preBackup}
            onChange={(e) => setPreBackup(e.target.value)}
            placeholder="./bin/flush"
            inputClassName="font-mono"
            description="Runs (sh -c) in a running replica before each backup of the volumes, e.g. to flush to disk. Optional."
          />
        )}
      </form>
      <SaveBar form={formId} dirty={dirty} saving={save.isPending} onReset={reset} label="Save volumes" />
    </Section>
  );
}

type AccessForm = {
  users: BasicAuthRow[];
  allow: string;
  average: string;
  burst: string;
  headers: { name: string; value: string }[];
};

function accessForm(m: Middlewares): AccessForm {
  return {
    users: (m.basicAuth ?? []).map((u) => ({ name: u.name, password: u.ref ?? "", stored: true })),
    allow: (m.ipAllowList ?? []).join("\n"),
    average: m.rateLimit ? String(m.rateLimit.average) : "",
    burst: m.rateLimit ? String(m.rateLimit.burst) : "",
    headers: Object.entries(m.headers ?? {})
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([name, value]) => ({ name, value })),
  };
}

function accessInput(f: AccessForm): MiddlewaresInput {
  const average = Number(f.average);
  return {
    basicAuth: f.users.map((u) => ({ name: u.name.trim(), password: u.password.trim() })),
    ipAllowList: f.allow.split(/[\s,]+/).filter(Boolean),
    rateLimit: f.average.trim() ? { average, burst: f.burst.trim() ? Number(f.burst) : average } : null,
    headers: Object.fromEntries(f.headers.filter((h) => h.name.trim()).map((h) => [h.name.trim(), h.value.trim()])),
  };
}

/** Traefik middlewares on the public domain: basic auth, IP allowlist, rate limit, response headers. */
function AccessCard({ svc }: { svc: ServiceT }) {
  const qc = useQueryClient();
  const [form, setForm] = useState(() => accessForm(svc.middlewares));
  const dirty = JSON.stringify(form) !== JSON.stringify(accessForm(svc.middlewares));
  const formId = useId();
  const save = useMutation({
    meta: { error: "Couldn't save the access settings" },
    mutationFn: () => api.updateService(svc.id, { middlewares: accessInput(form) }),
    onSuccess: (updated) => {
      qc.setQueryData(["service", svc.id], updated);
      setForm(accessForm(updated.middlewares));
      toast.success("Access settings saved", { description: "Deploy to apply." });
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };
  const setHeader = (i: number, patch: Partial<AccessForm["headers"][number]>) =>
    setForm({ ...form, headers: form.headers.map((h, j) => (j === i ? { ...h, ...patch } : h)) });

  return (
    <Section title="Access" description={<>Applied by Traefik to every request for <Mono>{svc.domain}</Mono>, from the next deploy.</>}>
      <form id={formId} onSubmit={onSubmit} className="space-y-6">
        <BasicAuthUsers rows={form.users} onChange={(users) => setForm({ ...form, users })} />

        <FloatingTextarea
          label="IP allowlist"
          value={form.allow}
          onChange={(e) => setForm({ ...form, allow: e.target.value })}
          placeholder={"203.0.113.7\n10.0.0.0/8"}
          className="[&_textarea]:font-mono"
          description="One IP or CIDR range per line. Empty allows everyone; otherwise other clients get 403."
        />

        <div className="space-y-3">
          <div>
            <h3 className="text-sm font-medium">Rate limit</h3>
            <p className="text-sm text-muted-foreground">Requests per second per client IP; over the limit, clients get 429. Empty means unlimited.</p>
          </div>
          <div className="flex gap-2">
            <FloatingInput
              label="Requests / s"
              type="number"
              min={1}
              value={form.average}
              onChange={(e) => setForm({ ...form, average: e.target.value })}
              className="w-40"
            />
            <FloatingInput
              label="Burst"
              type="number"
              min={1}
              value={form.burst}
              onChange={(e) => setForm({ ...form, burst: e.target.value })}
              placeholder={form.average || "Same as the rate"}
              className="w-40"
              disabled={!form.average.trim()}
            />
          </div>
        </div>

        <div className="space-y-3">
          <div className="flex items-center justify-between gap-2">
            <div>
              <h3 className="text-sm font-medium">Response headers</h3>
              <p className="text-sm text-muted-foreground">
                Set on every response, e.g. <Mono>X-Robots-Tag: noindex</Mono> for staging. An empty value removes the header.
              </p>
            </div>
            <Button type="button" size="sm" variant="outline" onClick={() => setForm({ ...form, headers: [...form.headers, { name: "", value: "" }] })}>
              <Plus data-icon="inline-start" />
              Add header
            </Button>
          </div>
          {form.headers.map((h, i) => (
            <div key={i} className="flex items-start gap-2">
              <FloatingInput
                label="Header"
                required
                value={h.name}
                onChange={(e) => setHeader(i, { name: e.target.value })}
                placeholder="X-Robots-Tag"
                className="w-40 shrink-0 sm:w-56"
                inputClassName="font-mono"
              />
              <FloatingInput
                label="Value"
                value={h.value}
                onChange={(e) => setHeader(i, { value: e.target.value })}
                placeholder="noindex"
                className="min-w-0 flex-1"
                inputClassName="font-mono"
              />
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                title="Remove"
                aria-label={`Remove ${h.name || "header"}`}
                className="mt-3.5 text-muted-foreground hover:text-destructive"
                onClick={() => setForm({ ...form, headers: form.headers.filter((_, j) => j !== i) })}
              >
                <Trash2 />
              </Button>
            </div>
          ))}
        </div>
      </form>
      <SaveBar form={formId} dirty={dirty} saving={save.isPending} onReset={() => setForm(accessForm(svc.middlewares))} label="Save access" />
    </Section>
  );
}

function EnvironmentCard({ svc }: { svc: ServiceT }) {
  const qc = useQueryClient();
  // A database's env is generated at creation: shown, never edited.
  const isDb = isDatabase(svc.kind);
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
          readOnly={isDb}
        />
      </form>
      {!isDb && <SaveBar form={formId} dirty={dirty} saving={save.isPending} onReset={() => setRows(envRows(svc.env, svc.secrets))} />}
    </Section>
  );
}

function BackupsCard({ serviceId, name, kind }: { serviceId: string; name: string; kind: ServiceT["kind"] }) {
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
    await api.backup(serviceId, targetId, database);
    qc.invalidateQueries({ queryKey: ["backups"] });
  };
  const lastRestore = restores.data?.[0];
  const busy = backups.data?.some((b) => b.status === "running") || lastRestore?.status === "running";

  return (
    <Section
      plain
      actions={<BackupNowDialog what={name} targets={targets.data ?? []} disabled={busy} onBackup={backup} databases={databases.data} />}
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

function DeployFromCICard({ svc }: { svc: ServiceT }) {
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
    <Section
      title="Deploy from CI"
      description={
        <>
          Build and push <Mono>{repo}</Mono> in CI, then deploy the tag it pushed with a <Mono>deploy</Mono> token (Access ›
          API tokens & MCP). The service keeps that tag; the step fails if the deployment does.
        </>
      }
    >
      <div className="relative">
        <pre className={cn(terminal, "pr-10")}>{step}</pre>
        <div className="absolute top-1.5 right-1.5 text-neutral-200">
          <CopyButton value={step} label="Copy step" />
        </div>
      </div>
    </Section>
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
          <Mono>{`${kind === "redis" ? "REDIS_URL" : "DATABASE_URL"}={{ db.${host}.URL }}`}</Mono>.
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

  if (deployments.length === 0)
    return <EmptyState icon={Rocket} title="No deployments yet" description="Deploy the service to see its builds and logs here." />;

  return (
    <div className="space-y-6">
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
                {d.error && <span className="text-destructive"> · {d.error}</span>}
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

/** Says why a git project's service settings are read-only. */
function GitManagedNote({ path }: { path: string }) {
  return (
    <p className="rounded-md border border-dashed px-3 py-2 text-sm text-muted-foreground">
      This project follows <Mono>{path}</Mono> in a git repository: change the service there. Deploying another image tag from CI still
      works until the file changes this service.
    </p>
  );
}
