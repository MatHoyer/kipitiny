import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { BackupList } from "./BackupList";
import { Schedules } from "./Schedules";
import { api, type Connection, type Deployment, type LogLine, type Service as ServiceT } from "../api";
import {
  Button,
  Card,
  confirmByName,
  ErrorText,
  Field,
  formatEnv,
  Input,
  parseEnv,
  Select,
  serviceState,
  StateBadge,
  Textarea,
  timeAgo,
} from "../ui";

export function Service() {
  const { id = "" } = useParams();
  const qc = useQueryClient();
  const navigate = useNavigate();

  const service = useQuery({ queryKey: ["service", id], queryFn: () => api.service(id), refetchInterval: 5_000 });
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
    mutationFn: () => api.deploy(id),
    onSuccess: (d) => {
      setSelected(d.id);
      refresh();
    },
  });
  const action = useMutation({
    mutationFn: (a: "start" | "stop" | "restart") => api.serviceAction(id, a),
    onSuccess: (svc) => qc.setQueryData(["service", id], svc),
  });
  const remove = useMutation({
    mutationFn: (confirm: string) => api.deleteService(id, confirm),
    onSuccess: () => navigate(`/projects/${service.data?.projectId}`),
  });

  if (service.error) return <ErrorText error={service.error} />;
  const svc = service.data;
  if (!svc) return <p className="text-sm text-zinc-500">Loading…</p>;

  const state = deploying ? "deploying" : serviceState(svc.containers.filter((c) => !c.retired));
  const active = svc.containers.filter((c) => !c.retired);
  const allStopped = active.length > 0 && active.every((c) => c.state !== "running");
  const busy = deploying || deploy.isPending || action.isPending || remove.isPending;

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <Link to={`/projects/${svc.projectId}`} className="text-xs text-zinc-500 hover:underline">
            ← Project
          </Link>
          <div className="flex items-center gap-3">
            <h1 className="text-xl font-semibold">{svc.name}</h1>
            <StateBadge state={state} />
          </div>
          {svc.domain && (
            <a href={`https://${svc.domain}`} target="_blank" rel="noreferrer" className="text-sm text-sky-600 hover:underline">
              {svc.domain}
            </a>
          )}
        </div>
        <div className="flex flex-wrap gap-2">
          <Button disabled={busy} onClick={() => deploy.mutate()}>
            {deploying ? "Deploying…" : "Deploy"}
          </Button>
          {svc.containers.length > 0 && (
            <>
              <Button variant="secondary" disabled={busy} onClick={() => action.mutate("restart")}>
                Restart
              </Button>
              <Button
                variant="secondary"
                disabled={busy}
                onClick={() => action.mutate(allStopped ? "start" : "stop")}
              >
                {allStopped ? "Start" : "Stop"}
              </Button>
            </>
          )}
          <Button
            variant="danger"
            disabled={busy}
            onClick={() => {
              if (svc.kind !== "postgres") {
                if (confirm(`Delete service ${svc.name} and its containers?`)) remove.mutate("");
                return;
              }
              const name = confirmByName(`Delete database ${svc.name}? Its data volume will be destroyed.`, svc.name);
              if (name) remove.mutate(name);
            }}
          >
            Delete
          </Button>
        </div>
      </div>
      <ErrorText error={deploy.error ?? action.error ?? remove.error} />

      <Card title="Containers">
        {svc.containers.length === 0 ? (
          <p className="text-sm text-zinc-500">Not deployed yet.</p>
        ) : (
          <table className="w-full text-left text-sm">
            <tbody className="divide-y divide-zinc-200 dark:divide-zinc-800">
              {svc.containers.map((c) => (
                <tr key={c.id} className={c.retired ? "text-zinc-400" : ""}>
                  <td className="py-1.5 font-mono text-xs">
                    {c.name}
                    {c.retired && <span className="ml-2 font-sans">previous deploy, kept for its logs</span>}
                  </td>
                  <td className="py-1.5">
                    <StateBadge state={c.health ?? c.state} />
                  </td>
                  <td className="py-1.5 text-xs text-zinc-500">{c.status}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Card>

      {svc.kind === "postgres" && <ConnectionCard serviceId={svc.id} host={svc.name} />}
      {svc.kind === "postgres" && <BackupsCard serviceId={svc.id} name={svc.name} />}

      {svc.containers.length > 0 && (
        // Remount (and reconnect) whenever the set of containers changes.
        <LiveLogs key={active.map((c) => c.id).join()} serviceId={id} />
      )}

      <div className="grid gap-6 lg:grid-cols-2">
        <Settings svc={svc} />
        <Deployments
          svc={svc}
          deployments={deployments.data ?? []}
          selected={selected}
          onSelect={setSelected}
          busy={busy}
        />
      </div>
    </div>
  );
}

function Settings({ svc }: { svc: ServiceT }) {
  const qc = useQueryClient();
  const isDb = svc.kind === "postgres";
  const siblings = useQuery({
    queryKey: ["services", svc.projectId],
    queryFn: () => api.services(svc.projectId),
    enabled: !isDb,
  });
  const databases = siblings.data?.filter((s) => s.kind === "postgres") ?? [];
  const [form, setForm] = useState(() => ({
    image: svc.image,
    domain: svc.domain,
    port: svc.port ? String(svc.port) : "",
    replicas: String(svc.replicas),
    memory: svc.memoryMb ? String(svc.memoryMb) : "",
    databaseId: svc.databaseId,
    healthPath: svc.healthPath,
    preDeploy: svc.preDeploy,
    env: formatEnv(svc.env),
  }));
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });

  const save = useMutation({
    mutationFn: () =>
      api.updateService(
        svc.id,
        isDb
          ? { image: form.image.trim(), memoryMb: Number(form.memory) || 0, env: parseEnv(form.env) }
          : {
              image: form.image.trim(),
              domain: form.domain.trim(),
              port: Number(form.port) || 0,
              replicas: Number(form.replicas) || 1,
              memoryMb: Number(form.memory) || 0,
              databaseId: form.databaseId,
              healthPath: form.healthPath.trim(),
              preDeploy: form.preDeploy.trim(),
              env: parseEnv(form.env),
            },
      ),
    onSuccess: (updated) => qc.setQueryData(["service", svc.id], updated),
  });

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <Card title="Settings">
      <form onSubmit={onSubmit} className="space-y-4">
        <Field label="Image" hint={isDb ? "Minor upgrades only; major versions need a dump and restore." : undefined}>
          <Input required value={form.image} onChange={set("image")} />
        </Field>
        {!isDb && (
          <div className="grid grid-cols-3 gap-3">
            <div className="col-span-3 sm:col-span-1">
              <Field label="Domain">
                <Input value={form.domain} onChange={set("domain")} placeholder="private" />
              </Field>
            </div>
            <Field label="Port">
              <Input type="number" min={0} max={65535} value={form.port} onChange={set("port")} />
            </Field>
            <Field label="Replicas">
              <Input type="number" min={1} max={10} value={form.replicas} onChange={set("replicas")} />
            </Field>
          </div>
        )}
        <div className="grid grid-cols-2 gap-3">
          <Field label="Memory (MB)" hint={isDb ? undefined : "Empty for no limit."}>
            <Input type="number" min={0} step={64} value={form.memory} onChange={set("memory")} />
          </Field>
          {!isDb && (
            <Field label="Database" hint="Injects DATABASE_URL.">
              <Select value={form.databaseId} onChange={set("databaseId")}>
                <option value="">None</option>
                {databases.map((d) => (
                  <option key={d.id} value={d.id}>
                    {d.name}
                  </option>
                ))}
              </Select>
            </Field>
          )}
        </div>
        {!isDb && (
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Health path" hint="New replicas get traffic once this answers < 400. Empty: port accepts connections.">
              <Input value={form.healthPath} onChange={set("healthPath")} placeholder="/healthz" />
            </Field>
            <Field label="Pre-deploy command" hint="Runs once (sh -c) before replicas start, e.g. migrations.">
              <Input value={form.preDeploy} onChange={set("preDeploy")} placeholder="npm run migrate" className="font-mono" />
            </Field>
          </div>
        )}
        <Field
          label="Environment"
          hint={
            isDb
              ? "POSTGRES_* credentials are fixed at creation."
              : "Hidden values (********) are kept as-is. Remove a line to delete a variable."
          }
        >
          <Textarea rows={5} value={form.env} onChange={set("env")} />
        </Field>
        <div className="flex items-center gap-3">
          <Button disabled={save.isPending}>Save</Button>
          {save.isSuccess && <span className="text-xs text-zinc-500">Saved. Deploy to apply.</span>}
          <ErrorText error={save.error} />
        </div>
      </form>
    </Card>
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
    mutationFn: () => api.backup(serviceId, targetId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["backups"] }),
  });
  const lastRestore = restores.data?.[0];
  const busy = backups.data?.some((b) => b.status === "running") || lastRestore?.status === "running";

  return (
    <Card
      title="Backups"
      actions={
        <>
          <Select value={targetId} onChange={(e) => setTargetId(e.target.value)} className="!w-auto !py-0.5 text-xs">
            {targets.data?.map((t) => (
              <option key={t.id} value={t.id}>
                {t.name}
              </option>
            ))}
          </Select>
          <Button className="!py-0.5 text-xs" disabled={busy || backup.isPending} onClick={() => backup.mutate()}>
            Back up now
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        {lastRestore && (
          <div className="flex items-center gap-2 text-xs text-zinc-500">
            Last restore: <StateBadge state={lastRestore.status} /> {timeAgo(lastRestore.createdAt)}
            {lastRestore.error && <span className="truncate text-red-600">{lastRestore.error}</span>}
          </div>
        )}
        <ErrorText error={backup.error} />
        <Schedules serviceId={serviceId} targets={targets.data ?? []} />
        <h3 className="pt-2 text-xs font-semibold tracking-wide text-zinc-500 uppercase">History</h3>
        <BackupList backups={backups.data ?? []} targets={targets.data ?? []} restoreInto={name} />
      </div>
    </Card>
  );
}

function ConnectionCard({ serviceId, host }: { serviceId: string; host: string }) {
  const [conn, setConn] = useState<Connection | null>(null);
  const reveal = useMutation({ mutationFn: () => api.connection(serviceId), onSuccess: setConn });
  const rows: [string, string][] = conn
    ? [
        ["Host", `${conn.host}:${conn.port}`],
        ["Database", conn.database],
        ["User", conn.user],
        ["Password", conn.password],
        ["URL", conn.url],
      ]
    : [];
  return (
    <Card
      title="Connection"
      actions={
        <Button
          variant="secondary"
          className="!py-0.5 text-xs"
          onClick={() => (conn ? setConn(null) : reveal.mutate())}
          disabled={reveal.isPending}
        >
          {conn ? "Hide" : "Reveal credentials"}
        </Button>
      }
    >
      {conn ? (
        <dl className="grid grid-cols-[6rem_1fr] gap-y-1.5 text-sm">
          {rows.map(([k, v]) => (
            <div key={k} className="contents">
              <dt className="text-zinc-500">{k}</dt>
              <dd className="flex items-center gap-2 font-mono text-xs break-all">
                {v}
                <button
                  type="button"
                  onClick={() => navigator.clipboard.writeText(v)}
                  className="text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-100"
                >
                  copy
                </button>
              </dd>
            </div>
          ))}
        </dl>
      ) : (
        <p className="text-sm text-zinc-500">
          Reachable only inside the project at <span className="font-mono">{host}:5432</span>. Link it
          from an app to inject <span className="font-mono">DATABASE_URL</span>.
        </p>
      )}
      <ErrorText error={reveal.error} />
    </Card>
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
    mutationFn: (deploymentId: string) => api.rollback(svc.id, deploymentId),
    onSuccess: (d) => {
      onSelect(d.id);
      qc.invalidateQueries({ queryKey: ["deployments", svc.id] });
    },
  });
  const current = deployments.find((d) => d.id === selected);
  return (
    <Card title="Deployments">
      {deployments.length === 0 ? (
        <p className="text-sm text-zinc-500">No deployments yet.</p>
      ) : (
        <div className="space-y-3">
          <ul className="max-h-60 divide-y divide-zinc-200 overflow-y-auto dark:divide-zinc-800">
            {deployments.map((d) => (
              <li key={d.id}>
                <button
                  onClick={() => onSelect(d.id === selected ? null : d.id)}
                  className={`flex w-full items-center justify-between gap-3 px-1 py-1.5 text-left text-sm hover:bg-zinc-50 dark:hover:bg-zinc-800 ${d.id === selected ? "bg-zinc-100 dark:bg-zinc-800" : ""}`}
                >
                  <StateBadge state={d.status} />
                  <span className="flex-1 truncate font-mono text-xs text-zinc-500">
                    {d.image}
                    {d.id === svc.currentDeploymentId && (
                      <span className="ml-2 font-sans text-emerald-600">current</span>
                    )}
                  </span>
                  {svc.kind === "app" && d.status === "succeeded" && d.id !== svc.currentDeploymentId && !busy && (
                    <span
                      role="button"
                      className="text-xs hover:underline"
                      onClick={(e) => {
                        e.stopPropagation();
                        if (
                          confirm(
                            `Roll back to ${d.image}? Current settings are kept; the next regular deploy uses the image in Settings again.`,
                          )
                        )
                          rollback.mutate(d.id);
                      }}
                    >
                      Roll back
                    </span>
                  )}
                  <span className="text-xs text-zinc-500">{timeAgo(d.createdAt)}</span>
                </button>
              </li>
            ))}
          </ul>
          <ErrorText error={rollback.error} />
          {current && <DeploymentLog deployment={current} />}
        </div>
      )}
    </Card>
  );
}

function DeploymentLog({ deployment }: { deployment: Deployment }) {
  const log = useQuery({
    queryKey: ["deployment-log", deployment.id],
    queryFn: () => api.deploymentLog(deployment.id),
    refetchInterval: deployment.status === "running" ? 1_000 : false,
  });
  return (
    <div className="space-y-2">
      {deployment.error && <p className="text-sm text-red-600 dark:text-red-400">{deployment.error}</p>}
      <pre className="max-h-80 overflow-auto rounded-md bg-zinc-950 p-3 text-xs leading-relaxed text-zinc-200">
        {log.data || "No output."}
      </pre>
    </div>
  );
}

const MAX_LINES = 1000;

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
    <Card
      title="Logs"
      actions={
        ended && (
          <Button variant="secondary" className="!py-0.5 text-xs" onClick={() => setAttempt((n) => n + 1)}>
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
        className="h-80 overflow-auto rounded-md bg-zinc-950 p-3 text-xs leading-relaxed text-zinc-200"
      >
        {lines.length === 0 && <span className="text-zinc-500">{ended ? "Stream closed." : "Waiting for logs…"}</span>}
        {lines.map((l, i) => (
          <div key={i}>
            {multi && <span className="text-zinc-500">{l.container} | </span>}
            {l.text}
          </div>
        ))}
      </pre>
    </Card>
  );
}
