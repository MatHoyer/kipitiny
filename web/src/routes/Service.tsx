import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ExternalLink, Play, RefreshCw, RotateCcw, Rocket, Square, Trash2 } from "lucide-react";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { useNavigate, useParams } from "react-router";
import { toast } from "sonner";
import { PostgresIcon } from "@/components/brand-icons";
import { Empty, ErrorText, Mono, SecretList, Section, StateBadge, Tag } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DomainField } from "@/components/domain-field";
import { EnvEditor } from "@/components/env-editor";
import { PageBody, PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { FloatingInput } from "@/components/ui/floating-input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useTab } from "@/hooks/use-tab";
import { envMap, envRows, envSecrets, serviceState, timeAgo, type EnvRow } from "@/lib/format";
import { cn } from "@/lib/utils";
import { api, type Connection, type Deployment, type LogLine, type Service as ServiceT } from "../api";
import { BackupList } from "./BackupList";
import { GitFields } from "./Project";
import { Schedules } from "./Schedules";

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

  const [tab, setTab] = useTab(["overview", "environment"], "overview");
  const svc = service.data;
  const crumbs = [
    { label: "Projects", to: "/" },
    { label: project.data?.name ?? "…", to: svc && `/projects/${svc.projectId}` },
    { label: svc?.name ?? "…" },
  ];
  if (!svc)
    return (
      <>
        <PageHeader crumbs={crumbs} />
        <PageBody>{service.error ? <ErrorText error={service.error} /> : <Empty>Loading…</Empty>}</PageBody>
      </>
    );

  const active = svc.containers.filter((c) => !c.retired);
  const state = deploying ? "deploying" : svc.stopped ? "stopped" : serviceState(active);
  const allStopped = active.length > 0 && active.every((c) => c.state !== "running");
  const busy = deploying || deploy.isPending || action.isPending || remove.isPending;
  const isDb = svc.kind === "postgres";

  return (
    <>
      <PageHeader
        crumbs={crumbs}
        actions={
          <>
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
            <Button size="sm" disabled={busy} onClick={() => deploy.mutate()}>
              <Rocket data-icon="inline-start" />
              {deploying ? "Deploying…" : "Deploy"}
            </Button>
            <ConfirmDialog
              trigger={
                <Button variant="destructive" size="icon-sm" aria-label="Delete service" disabled={busy}>
                  <Trash2 />
                </Button>
              }
              title={isDb ? `Delete database ${svc.name}?` : `Delete service ${svc.name}?`}
              description={isDb ? "Its data volume will be destroyed." : "Its containers are removed."}
              typeToConfirm={isDb ? svc.name : undefined}
              onConfirm={(name) => remove.mutate(name)}
            />
          </>
        }
      />
      <PageBody>
        <div className="flex flex-wrap items-center gap-3">
          {isDb && <PostgresIcon aria-label="PostgreSQL" className="size-5 text-[#4169E1]" />}
          <StateBadge state={state} />
          {svc.domain && (
            <a
              href={`https://${svc.domain}`}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-1 text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
            >
              {svc.domain}
              <ExternalLink className="size-3.5" />
            </a>
          )}
          {svc.dns?.state === "synced" && <span className="text-xs text-muted-foreground">DNS managed by kipitiny</span>}
        </div>
        {svc.dns && svc.dns.state !== "synced" && (
          <p role="alert" className="text-sm text-destructive">
            DNS {svc.dns.state}: {svc.dns.message}
          </p>
        )}

        <Tabs value={tab} onValueChange={setTab}>
          <TabsList variant="line">
            <TabsTrigger value="overview">Overview</TabsTrigger>
            <TabsTrigger value="environment">Environment</TabsTrigger>
          </TabsList>
          <TabsContent value="overview" className="space-y-4">
            <Section title="Containers">
              {svc.containers.length === 0 ? (
                <Empty>Not deployed yet.</Empty>
              ) : (
                <ul className="-my-2 divide-y">
                  {svc.containers.map((c) => (
                    <li key={c.id} className={cn("flex flex-wrap items-center gap-x-3 gap-y-1 py-2", c.retired && "opacity-50")}>
                      <span className="font-mono text-xs">{c.name}</span>
                      <StateBadge state={c.health ?? c.state} />
                      <span className="flex-1 text-right text-xs text-muted-foreground">
                        {c.retired ? "previous deploy, kept for its logs" : c.status}
                      </span>
                    </li>
                  ))}
                </ul>
              )}
            </Section>

            {isDb && <ConnectionCard serviceId={svc.id} host={svc.name} />}
            {svc.source === "git" && <WebhookCard serviceId={svc.id} branch={svc.gitBranch} />}
            {isDb && <BackupsCard serviceId={svc.id} name={svc.name} />}

            {svc.containers.length > 0 && (
              // Remount (and reconnect) whenever the set of containers changes.
              <LiveLogs key={active.map((c) => c.id).join()} serviceId={id} />
            )}

            <div className="grid items-start gap-4 lg:grid-cols-2">
              <Settings svc={svc} />
              <Deployments svc={svc} deployments={deployments.data ?? []} selected={selected} onSelect={setSelected} busy={busy} />
            </div>
          </TabsContent>
          <TabsContent value="environment">
            <EnvironmentCard key={svc.id} svc={svc} />
          </TabsContent>
        </Tabs>
      </PageBody>
    </>
  );
}

function Settings({ svc }: { svc: ServiceT }) {
  const qc = useQueryClient();
  const isDb = svc.kind === "postgres";
  const [form, setForm] = useState(() => ({
    image: svc.image,
    domain: svc.domain,
    port: svc.port ? String(svc.port) : "",
    replicas: String(svc.replicas),
    memory: svc.memoryMb ? String(svc.memoryMb) : "",
    healthPath: svc.healthPath,
    preDeploy: svc.preDeploy,
    gitUrl: svc.gitUrl,
    gitBranch: svc.gitBranch,
    gitToken: svc.gitToken,
    dockerfile: svc.dockerfile,
    buildContext: svc.buildContext,
  }));
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });

  const save = useMutation({
    meta: { error: "Couldn't save the settings" },
    mutationFn: () =>
      api.updateService(
        svc.id,
        isDb
          ? { image: form.image.trim(), memoryMb: Number(form.memory) || 0, }
          : {
              ...(svc.source === "git"
                ? {
                    gitUrl: form.gitUrl.trim(),
                    gitBranch: form.gitBranch.trim(),
                    gitToken: form.gitToken,
                    dockerfile: form.dockerfile.trim(),
                    buildContext: form.buildContext.trim(),
                  }
                : { image: form.image.trim() }),
              domain: form.domain.trim(),
              port: Number(form.port) || 0,
              replicas: Number(form.replicas) || 1,
              memoryMb: Number(form.memory) || 0,
              healthPath: form.healthPath.trim(),
              preDeploy: form.preDeploy.trim(),
            },
      ),
    onSuccess: (updated) => {
      qc.setQueryData(["service", svc.id], updated);
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
    <Section title="Settings">
      <form onSubmit={onSubmit} className="grid gap-4 sm:grid-cols-2">
        {svc.source === "git" ? (
          <GitFields form={form} set={set} tokenHint="******** keeps the saved token." />
        ) : (
          <FloatingInput
            label="Image"
            required
            value={form.image}
            onChange={set("image")}
            className="sm:col-span-2"
            description={isDb ? "Minor upgrades only; major versions need a dump and restore." : undefined}
          />
        )}
        {!isDb && (
          <>
            <DomainField value={form.domain} onChange={(domain) => setForm({ ...form, domain })} className="sm:col-span-2" />
            <FloatingInput label="Port" type="number" min={0} max={65535} value={form.port} onChange={set("port")} />
            <FloatingInput label="Replicas" type="number" min={1} max={10} value={form.replicas} onChange={set("replicas")} />
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
          className="sm:col-span-2"
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
          </>
        )}
        <div className="flex items-center justify-end gap-3 sm:col-span-2">
          <Button type="submit" disabled={save.isPending}>
            {save.isPending ? "Saving…" : "Save"}
          </Button>
        </div>
      </form>
    </Section>
  );
}

function EnvironmentCard({ svc }: { svc: ServiceT }) {
  const qc = useQueryClient();
  const isDb = svc.kind === "postgres";
  const project = useQuery({ queryKey: ["project", svc.projectId], queryFn: () => api.project(svc.projectId) });
  const siblings = useQuery({ queryKey: ["services", svc.projectId], queryFn: () => api.services(svc.projectId) });
  const databases = siblings.data?.filter((s) => s.kind === "postgres" && s.id !== svc.id).map((s) => s.name);
  const [rows, setRows] = useState<EnvRow[]>(() => envRows(svc.env, svc.secrets));
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
      title="Environment"
      description={
        <>
          Use the project&apos;s shared entries with <Mono>{"{{ project.NAME }}"}</Mono> and its databases with{" "}
          <Mono>{"{{ db.NAME.URL }}"}</Mono>. Changes apply on the next deploy.
        </>
      }
    >
      <form onSubmit={onSubmit} className="space-y-4">
        <EnvEditor
          showLabel={false}
          rows={rows}
          onChange={setRows}
          vars={Object.keys(project.data?.env ?? {}).sort()}
          secretVars={project.data?.secrets}
          databases={databases}
          description={isDb ? "POSTGRES_* credentials are fixed at creation." : undefined}
        />
        <div className="flex items-center justify-end gap-3">
          <Button type="submit" disabled={save.isPending}>
            {save.isPending ? "Saving…" : "Save"}
          </Button>
        </div>
      </form>
    </Section>
  );
}

function BackupsCard({ serviceId, name }: { serviceId: string; name: string }) {
  const qc = useQueryClient();
  const targets = useQuery({ queryKey: ["backup-targets"], queryFn: api.backupTargets });
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
  const [targetId, setTargetId] = useState("local");
  const backup = useMutation({
    meta: { error: "Couldn't start the backup" },
    mutationFn: () => api.backup(serviceId, targetId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["backups"] }),
  });
  const lastRestore = restores.data?.[0];
  const busy = backups.data?.some((b) => b.status === "running") || lastRestore?.status === "running";

  return (
    <Section
      title="Backups"
      actions={<BackupNow targets={targets.data ?? []} targetId={targetId} onTarget={setTargetId} disabled={busy || backup.isPending} onBackup={() => backup.mutate()} />}
    >
      {lastRestore && (
        <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
          Last restore <StateBadge state={lastRestore.status} /> {timeAgo(lastRestore.createdAt)}
          {lastRestore.error && <span className="truncate text-destructive">{lastRestore.error}</span>}
        </div>
      )}
      <Schedules serviceId={serviceId} targets={targets.data ?? []} />
      <div className="space-y-2">
        <h3 className="text-sm font-medium">History</h3>
        <BackupList backups={backups.data ?? []} targets={targets.data ?? []} restoreInto={name} />
      </div>
    </Section>
  );
}

/** Target picker and "Back up now", for a card's actions. */
export function BackupNow({
  targets,
  targetId,
  onTarget,
  disabled,
  onBackup,
}: {
  targets: { id: string; name: string }[];
  targetId: string;
  onTarget: (id: string) => void;
  disabled?: boolean;
  onBackup: () => void;
}) {
  return (
    <>
      <Select value={targetId} onValueChange={onTarget}>
        <SelectTrigger size="sm" aria-label="Target" className="min-w-24">
          <SelectValue />
        </SelectTrigger>
        <SelectContent position="popper" align="end">
          {targets.map((t) => (
            <SelectItem key={t.id} value={t.id}>
              {t.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Button size="sm" disabled={disabled} onClick={onBackup}>
        Back up now
      </Button>
    </>
  );
}

function WebhookCard({ serviceId, branch }: { serviceId: string; branch: string }) {
  const [hook, setHook] = useState<{ url: string; secret: string } | null>(null);
  const reveal = useMutation({ meta: { error: "Couldn't show the webhook" }, mutationFn: () => api.webhook(serviceId), onSuccess: setHook });
  return (
    <Section
      title="Deploy on push"
      description={
        <>
          Add a push webhook (GitHub: content type <Mono>application/json</Mono>; GitLab: secret token) and every push to{" "}
          <Mono>{branch}</Mono> builds and deploys.
        </>
      }
      actions={
        <Button variant="outline" size="sm" onClick={() => (hook ? setHook(null) : reveal.mutate())} disabled={reveal.isPending}>
          {hook ? "Hide" : "Show webhook"}
        </Button>
      }
    >
      {hook && (
        <SecretList
          rows={[
            ["Payload URL", window.location.origin + hook.url],
            ["Secret", hook.secret],
          ]}
        />
      )}
    </Section>
  );
}

function ConnectionCard({ serviceId, host }: { serviceId: string; host: string }) {
  const [conn, setConn] = useState<Connection | null>(null);
  const reveal = useMutation({ meta: { error: "Couldn't show the connection details" }, mutationFn: () => api.connection(serviceId), onSuccess: setConn });
  return (
    <Section
      title="Connection"
      description={
        <>
          Reachable only inside the project at <Mono>{host}:5432</Mono>. Apps use it from their Environment
          (Secrets → Connect database), e.g. <Mono>{`DATABASE_URL={{ db.${host}.URL }}`}</Mono>.
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
            ["Database", conn.database],
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
  selected,
  onSelect,
  busy,
}: {
  svc: ServiceT;
  deployments: Deployment[];
  selected: string | null;
  onSelect: (id: string | null) => void;
  busy: boolean;
}) {
  const qc = useQueryClient();
  const rollback = useMutation({
    meta: { error: "Couldn't roll back" },
    mutationFn: (deploymentId: string) => api.rollback(svc.id, deploymentId),
    onSuccess: (d) => {
      onSelect(d.id);
      qc.invalidateQueries({ queryKey: ["deployments", svc.id] });
    },
  });
  const current = deployments.find((d) => d.id === selected);
  return (
    <Section title="Deployments" description={deployments.length > 0 ? "Select one to see its log." : undefined}>
      {deployments.length === 0 ? (
        <Empty>No deployments yet.</Empty>
      ) : (
        <>
          <ul className="-mx-2 max-h-72 space-y-0.5 overflow-y-auto">
            {deployments.map((d) => (
              <li
                key={d.id}
                className={cn(
                  "flex items-center gap-2 rounded-lg px-2 py-1.5 text-sm hover:bg-muted",
                  d.id === selected && "bg-muted",
                )}
              >
                <button
                  type="button"
                  onClick={() => onSelect(d.id === selected ? null : d.id)}
                  className="flex min-w-0 flex-1 items-center gap-3 text-left outline-none"
                >
                  <StateBadge state={d.status} />
                  <span className="flex-1 truncate font-mono text-xs text-muted-foreground">
                    {d.gitCommit ? `commit ${d.gitCommit.slice(0, 7)}` : d.image}
                  </span>
                  {d.id === svc.currentDeploymentId && <Tag className="bg-emerald-500/10 text-emerald-600">current</Tag>}
                  <span className="text-xs whitespace-nowrap text-muted-foreground">{timeAgo(d.createdAt)}</span>
                </button>
                {svc.kind === "app" && d.status === "succeeded" && d.id !== svc.currentDeploymentId && !busy && (
                  <ConfirmDialog
                    trigger={
                      <Button variant="ghost" size="icon-xs" aria-label="Roll back" title="Roll back">
                        <RefreshCw />
                      </Button>
                    }
                    title="Roll back?"
                    description={`Redeploys ${d.image}. Current settings are kept; the next regular deploy uses the image in Settings again.`}
                    confirmLabel="Roll back"
                    destructive={false}
                    onConfirm={() => rollback.mutate(d.id)}
                  />
                )}
              </li>
            ))}
          </ul>
          {current && <DeploymentLog deployment={current} />}
        </>
      )}
    </Section>
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
    <div className="space-y-2">
      {deployment.error && <p className="text-sm text-destructive">{deployment.error}</p>}
      <pre className={cn(terminal, "max-h-80")}>{log.data || "No output."}</pre>
    </div>
  );
}

const MAX_LINES = 1000;

// Local HH:MM:SS; lines Docker didn't timestamp come with the zero time.
function logTime(t: string) {
  const d = new Date(t);
  return d.getFullYear() > 1 ? d.toLocaleTimeString([], { hour12: false }) : "--:--:--";
}

function LiveLogs({ serviceId }: { serviceId: string }) {
  const [lines, setLines] = useState<LogLine[]>([]);
  const [ended, setEnded] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const box = useRef<HTMLPreElement>(null);
  const stick = useRef(true);

  useEffect(() => {
    setLines([]);
    setEnded(false);
    const es = new EventSource(api.logsUrl(serviceId));
    es.onmessage = (e) => {
      const line = JSON.parse(e.data) as LogLine;
      setLines((prev) => (prev.length >= MAX_LINES ? [...prev.slice(-MAX_LINES + 1), line] : [...prev, line]));
    };
    // The server sends "end" when every container stream closed.
    es.addEventListener("end", () => {
      es.close();
      setEnded(true);
    });
    es.onerror = () => {
      es.close();
      setEnded(true);
    };
    return () => es.close();
  }, [serviceId, attempt]);

  useEffect(() => {
    if (stick.current && box.current) box.current.scrollTop = box.current.scrollHeight;
  }, [lines]);

  const multi = new Set(lines.map((l) => l.container)).size > 1;

  return (
    <Section
      title="Logs"
      actions={
        ended && (
          <Button variant="outline" size="sm" onClick={() => setAttempt((n) => n + 1)}>
            Reconnect
          </Button>
        )
      }
    >
      <pre
        ref={box}
        onScroll={(e) => {
          const el = e.currentTarget;
          stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 20;
        }}
        className={cn(terminal, "h-80")}
      >
        {lines.length === 0 && <span className="text-neutral-500">{ended ? "Stream closed." : "Waiting for logs…"}</span>}
        {lines.map((l, i) => (
          <div key={i}>
            <span className="text-neutral-500">{logTime(l.time)} </span>
            {multi && <span className="text-neutral-500">{l.container} | </span>}
            {l.text}
          </div>
        ))}
      </pre>
    </Section>
  );
}
