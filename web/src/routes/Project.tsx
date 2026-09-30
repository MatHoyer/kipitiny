import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { api } from "../api";
import { Button, Card, ErrorText, Field, Input, parseEnv, serviceState, StateBadge, Textarea } from "../ui";

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

  const remove = useMutation({
    mutationFn: () => api.deleteProject(id),
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
          <Button onClick={() => setShowForm((v) => !v)}>{showForm ? "Cancel" : "New service"}</Button>
          <Button
            variant="danger"
            disabled={remove.isPending}
            onClick={() =>
              confirm(`Delete project ${project.data.name}? All its containers will be removed.`) && remove.mutate()
            }
          >
            Delete
          </Button>
        </div>
      </div>
      <ErrorText error={remove.error} />

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
                {s.domain || "private"} · {s.replicas} replica{s.replicas > 1 ? "s" : ""}
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
  const [form, setForm] = useState({ name: "", image: "", port: "", domain: "", replicas: "1", env: "" });
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });

  const create = useMutation({
    mutationFn: async () => {
      const svc = await api.createService(projectId, {
        name: form.name.trim(),
        image: form.image.trim(),
        port: Number(form.port) || 0,
        domain: form.domain.trim(),
        replicas: Number(form.replicas) || 1,
        env: parseEnv(form.env),
      });
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
    <Card title="New app service">
      <form onSubmit={onSubmit} className="grid gap-4 sm:grid-cols-2">
        <Field label="Name">
          <Input required value={form.name} onChange={set("name")} placeholder="web" />
        </Field>
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
        <div className="sm:col-span-2">
          <Field label="Environment" hint="One KEY=value per line. Values are hidden once saved.">
            <Textarea rows={4} value={form.env} onChange={set("env")} placeholder="NODE_ENV=production" />
          </Field>
        </div>
        <div className="flex items-center gap-3 sm:col-span-2">
          <Button disabled={create.isPending}>{create.isPending ? "Creating…" : "Create & deploy"}</Button>
          <ErrorText error={create.error} />
        </div>
      </form>
    </Card>
  );
}
