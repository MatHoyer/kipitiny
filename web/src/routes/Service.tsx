import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { api, type Deployment, type LogLine, type Service as ServiceT } from "../api";
import {
  Button,
  Card,
  ErrorText,
  Field,
  formatEnv,
  Input,
  parseEnv,
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
    mutationFn: () => api.deleteService(id),
    onSuccess: () => navigate(`/projects/${service.data?.projectId}`),
  });

  if (service.error) return <ErrorText error={service.error} />;
  const svc = service.data;
  if (!svc) return <p className="text-sm text-zinc-500">Loading…</p>;

  const state = deploying ? "deploying" : serviceState(svc.containers);
  const allStopped = svc.containers.length > 0 && svc.containers.every((c) => c.state !== "running");
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
            onClick={() => confirm(`Delete service ${svc.name} and its containers?`) && remove.mutate()}
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
                <tr key={c.id}>
                  <td className="py-1.5 font-mono text-xs">{c.name}</td>
                  <td className="py-1.5">
                    <StateBadge state={c.state} />
                  </td>
                  <td className="py-1.5 text-xs text-zinc-500">{c.status}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Card>

      {svc.containers.length > 0 && (
        // Remount (and reconnect) whenever the set of containers changes.
        <LiveLogs key={svc.containers.map((c) => c.id).join()} serviceId={id} />
      )}

      <div className="grid gap-6 lg:grid-cols-2">
        <Settings svc={svc} />
        <Deployments deployments={deployments.data ?? []} selected={selected} onSelect={setSelected} />
      </div>
    </div>
  );
}

function Settings({ svc }: { svc: ServiceT }) {
  const qc = useQueryClient();
  const initial = () => ({
    image: svc.image,
    domain: svc.domain,
    port: svc.port ? String(svc.port) : "",
    replicas: String(svc.replicas),
    env: formatEnv(svc.env),
  });
  const [form, setForm] = useState(initial);
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });

  const save = useMutation({
    mutationFn: () =>
      api.updateService(svc.id, {
        image: form.image.trim(),
        domain: form.domain.trim(),
        port: Number(form.port) || 0,
        replicas: Number(form.replicas) || 1,
        env: parseEnv(form.env),
      }),
    onSuccess: (updated) => qc.setQueryData(["service", svc.id], updated),
  });

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <Card title="Settings">
      <form onSubmit={onSubmit} className="space-y-4">
        <Field label="Image">
          <Input required value={form.image} onChange={set("image")} />
        </Field>
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
        <Field label="Environment" hint="Hidden values (********) are kept as-is. Remove a line to delete a variable.">
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

function Deployments({
  deployments,
  selected,
  onSelect,
}: {
  deployments: Deployment[];
  selected: string | null;
  onSelect: (id: string | null) => void;
}) {
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
                  <span className="flex-1 truncate font-mono text-xs text-zinc-500">{d.image}</span>
                  <span className="text-xs text-zinc-500">{timeAgo(d.createdAt)}</span>
                </button>
              </li>
            ))}
          </ul>
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
