import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { api, type ServiceInput } from "../api";
import {
  Button,
  Card,
  confirmByName,
  ErrorText,
  Field,
  Input,
  parseEnv,
  Select,
  serviceState,
  StateBadge,
  Textarea,
} from "../ui";

export function Project() {
  const { id = "" } = useParams();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const project = useQuery({ queryKey: ["project", id], queryFn: () => api.project(id) });
  const services = useQuery({
    queryKey: ["services", id],
    queryFn: () => api.services(id),
    refetchInterval: 5_000,
  });
  const [showForm, setShowForm] = useState(false);

  const backupAll = useMutation({
    mutationFn: () => api.backupProject(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["backups"] }),
  });
  const hasDatabases = services.data?.some((s) => s.kind === "postgres");
  const remove = useMutation({
    mutationFn: (confirm: string) => api.deleteProject(id, confirm),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["projects"] });
      navigate("/");
    },
  });

  if (project.error) return <ErrorText error={project.error} />;
  if (!project.data) return <p className="text-sm text-zinc-500">Loading…</p>;

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <Link to="/" className="text-xs text-zinc-500 hover:underline">
            Projects
          </Link>
          <h1 className="text-xl font-semibold">{project.data.name}</h1>
        </div>
        <div className="flex gap-2">
          {hasDatabases && (
            <Button variant="secondary" disabled={backupAll.isPending} onClick={() => backupAll.mutate()}>
              {backupAll.isSuccess ? "Backups started" : "Back up databases"}
            </Button>
          )}
          <Button onClick={() => setShowForm((v) => !v)}>{showForm ? "Cancel" : "New service"}</Button>
          <Button
            variant="danger"
            disabled={remove.isPending}
            onClick={() => {
              const name = confirmByName(
                `Delete project ${project.data.name}? All its containers and database data will be destroyed.`,
                project.data.name,
              );
              if (name) remove.mutate(name);
            }}
          >
            Delete
          </Button>
        </div>
      </div>
      <ErrorText error={remove.error ?? backupAll.error} />
      {backupAll.data?.error && (
        <p className="text-sm text-amber-600">Some databases were not backed up: {backupAll.data.error}</p>
      )}

      {showForm && <NewServiceForm projectId={id} onDone={() => setShowForm(false)} />}

      {services.error ? (
        <ErrorText error={services.error} />
      ) : services.data?.length === 0 && !showForm ? (
        <p className="text-sm text-zinc-500">No services yet.</p>
      ) : (
        <div className="grid gap-3 sm:grid-cols-2">
          {services.data?.map((s) => (
            <Link
              key={s.id}
              to={`/services/${s.id}`}
              className="space-y-2 rounded-lg border border-zinc-200 bg-white p-4 hover:border-zinc-400 dark:border-zinc-800 dark:bg-zinc-900 dark:hover:border-zinc-600"
            >
              <div className="flex items-center justify-between">
                <span className="font-medium">{s.name}</span>
                <StateBadge state={serviceState(s.containers)} />
              </div>
              <p className="truncate font-mono text-xs text-zinc-500">{s.image}</p>
              <p className="text-xs text-zinc-500">
                {s.kind === "postgres"
                  ? `PostgreSQL · ${s.memoryMb} MB`
                  : `${s.domain || "private"} · ${s.replicas} replica${s.replicas > 1 ? "s" : ""}`}
              </p>
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}

function NewServiceForm({ projectId, onDone }: { projectId: string; onDone: () => void }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const services = useQuery({ queryKey: ["services", projectId], queryFn: () => api.services(projectId) });
  const databases = services.data?.filter((s) => s.kind === "postgres") ?? [];

  const [kind, setKind] = useState<"app" | "postgres">("app");
  const [form, setForm] = useState({
    name: "",
    image: "",
    port: "",
    domain: "",
    replicas: "1",
    env: "",
    version: "17",
    memory: "512",
    databaseId: "",
  });
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });
  // Default an app to the project's only database.
  const databaseId = form.databaseId || (databases.length === 1 ? databases[0].id : "");

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
              image: form.image.trim(),
              port: Number(form.port) || 0,
              domain: form.domain.trim(),
              replicas: Number(form.replicas) || 1,
              env: parseEnv(form.env),
              databaseId: databaseId === "none" ? "" : databaseId,
            };
      const svc = await api.createService(projectId, input);
      await api.deploy(svc.id);
      return svc;
    },
    onSuccess: (svc) => {
      qc.invalidateQueries({ queryKey: ["services", projectId] });
      onDone();
      navigate(`/services/${svc.id}`);
    },
  });

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    create.mutate();
  };

  return (
    <Card
      title="New service"
      actions={
        <div className="flex rounded-md border border-zinc-300 p-0.5 text-xs dark:border-zinc-700">
          {(["app", "postgres"] as const).map((k) => (
            <button
              key={k}
              type="button"
              onClick={() => setKind(k)}
              className={`rounded px-2 py-0.5 ${kind === k ? "bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900" : ""}`}
            >
              {k === "app" ? "App" : "PostgreSQL"}
            </button>
          ))}
        </div>
      }
    >
      <form onSubmit={onSubmit} className="grid gap-4 sm:grid-cols-2">
        <Field label="Name" hint={kind === "postgres" ? "Also the hostname apps use to connect." : undefined}>
          <Input required value={form.name} onChange={set("name")} placeholder={kind === "postgres" ? "db" : "web"} />
        </Field>
        {kind === "postgres" ? (
          <>
            <Field label="Version">
              <Select value={form.version} onChange={set("version")}>
                {["18", "17", "16", "15"].map((v) => (
                  <option key={v} value={v}>
                    PostgreSQL {v}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label="Memory (MB)" hint="Container limit; shared_buffers is sized from it.">
              <Input type="number" min={128} step={128} value={form.memory} onChange={set("memory")} />
            </Field>
          </>
        ) : (
          <>
            <Field label="Image">
              <Input required value={form.image} onChange={set("image")} placeholder="ghcr.io/org/app:latest" />
            </Field>
            <Field label="Domain" hint="Leave empty for a private service (reachable only inside the project).">
              <Input value={form.domain} onChange={set("domain")} placeholder="app.example.com" />
            </Field>
            <Field label="Container port" hint="The port your app listens on. Required with a domain.">
              <Input type="number" min={1} max={65535} value={form.port} onChange={set("port")} placeholder="3000" />
            </Field>
            <Field label="Replicas">
              <Input type="number" min={1} max={10} value={form.replicas} onChange={set("replicas")} />
            </Field>
            <Field label="Database" hint="Injects DATABASE_URL.">
              <Select value={databaseId || "none"} onChange={set("databaseId")}>
                <option value="none">None</option>
                {databases.map((d) => (
                  <option key={d.id} value={d.id}>
                    {d.name}
                  </option>
                ))}
              </Select>
            </Field>
            <div className="sm:col-span-2">
              <Field label="Environment" hint="One KEY=value per line. Values are hidden once saved.">
                <Textarea rows={4} value={form.env} onChange={set("env")} placeholder="NODE_ENV=production" />
              </Field>
            </div>
          </>
        )}
        <div className="flex items-center gap-3 sm:col-span-2">
          <Button disabled={create.isPending}>{create.isPending ? "Creating…" : "Create & deploy"}</Button>
          <ErrorText error={create.error} />
        </div>
      </form>
    </Card>
  );
}
