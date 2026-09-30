import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { api, type Server, type ServerInput } from "../api";
import { Button, Card, ErrorText, Field, Input, StateBadge } from "../ui";

export function Servers() {
  const qc = useQueryClient();
  const servers = useQuery({ queryKey: ["servers"], queryFn: api.servers, refetchInterval: 30_000 });
  const [editing, setEditing] = useState<Server | "new" | null>(null);
  const remove = useMutation({
    mutationFn: api.deleteServer,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["servers"] }),
  });

  return (
    <Card
      title="Servers"
      actions={
        !editing && (
          <Button variant="secondary" className="!py-0.5 text-xs" onClick={() => setEditing("new")}>
            Add server
          </Button>
        )
      }
    >
      <ul className="divide-y divide-zinc-200 text-sm dark:divide-zinc-800">
        {servers.data?.map((s) => (
          <li key={s.id} className="flex flex-wrap items-center justify-between gap-3 py-2">
            <div>
              <p className="flex items-center gap-2 font-medium">
                {s.name}
                <StateBadge state={s.docker ? "running" : "failed"} />
              </p>
              <p className="text-xs text-zinc-500">
                {s.kind === "local" ? "the manager's own Docker" : `ssh://${s.sshUser}@${s.host}:${s.port}`}
                {s.docker ? ` · Docker ${s.docker.version} (${s.docker.arch})` : ` · ${s.dockerError}`}
                {` · ${s.projects} project${s.projects === 1 ? "" : "s"}`}
              </p>
            </div>
            {s.kind === "ssh" && (
              <div className="space-x-3 text-xs">
                <button className="hover:underline" onClick={() => setEditing(s)}>
                  Edit
                </button>
                <button
                  className="text-red-600 hover:underline"
                  onClick={() => confirm(`Remove server ${s.name}?`) && remove.mutate(s.id)}
                >
                  Remove
                </button>
              </div>
            )}
          </li>
        ))}
      </ul>
      <ErrorText error={remove.error} />
      {editing && (
        <ServerForm
          key={editing === "new" ? "new" : editing.id}
          server={editing === "new" ? null : editing}
          onDone={() => setEditing(null)}
        />
      )}
    </Card>
  );
}

function ServerForm({ server, onDone }: { server: Server | null; onDone: () => void }) {
  const qc = useQueryClient();
  const key = useQuery({ queryKey: ["ssh-key"], queryFn: api.sshKey });
  const [form, setForm] = useState({
    name: server?.name ?? "",
    host: server?.host ?? "",
    port: String(server?.port ?? 22),
    sshUser: server?.sshUser ?? "root",
    socket: server?.socket ?? "/var/run/docker.sock",
  });
  const [resetHostKey, setResetHostKey] = useState(false);
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });
  const save = useMutation({
    mutationFn: () => {
      const input: ServerInput = { ...form, port: Number(form.port) || 22, resetHostKey };
      return server ? api.updateServer(server.id, input) : api.createServer(input);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["servers"] });
      onDone();
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <form onSubmit={onSubmit} className="mt-4 space-y-4 border-t border-zinc-200 pt-4 dark:border-zinc-800">
      <div className="space-y-1 text-sm">
        <p>
          On the server, add the manager's key to <span className="font-mono">~/.ssh/authorized_keys</span> of the SSH
          user (who needs access to the Docker socket):
        </p>
        <code className="block rounded bg-zinc-100 px-2 py-1 font-mono text-xs break-all dark:bg-zinc-800">
          {key.data?.publicKey ?? "…"}
        </code>
        <p className="text-xs text-zinc-500">
          sshd must allow <span className="font-mono">AllowTcpForwarding yes</span> (or{" "}
          <span className="font-mono">local</span>) and <span className="font-mono">AllowStreamLocalForwarding yes</span>
          : the Docker API is reached through the socket, never over TCP. The server's host key is pinned on first
          connection.
        </p>
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Name">
          <Input required value={form.name} onChange={set("name")} placeholder="db-server-2" />
        </Field>
        <Field label="Host">
          <Input required value={form.host} onChange={set("host")} placeholder="203.0.113.10" />
        </Field>
        <Field label="SSH port">
          <Input type="number" min={1} max={65535} value={form.port} onChange={set("port")} />
        </Field>
        <Field label="SSH user">
          <Input required value={form.sshUser} onChange={set("sshUser")} />
        </Field>
        <Field label="Docker socket">
          <Input required value={form.socket} onChange={set("socket")} className="font-mono" />
        </Field>
        {server?.hostKey && (
          <label className="flex items-start gap-2 self-end pb-2 text-sm">
            <input type="checkbox" className="mt-1" checked={resetHostKey} onChange={(e) => setResetHostKey(e.target.checked)} />
            <span>
              Forget the pinned host key
              <span className="block text-xs text-zinc-500">Only after reinstalling the server.</span>
            </span>
          </label>
        )}
      </div>
      <div className="flex items-center gap-3">
        <Button disabled={save.isPending}>{save.isPending ? "Connecting…" : server ? "Save" : "Add server"}</Button>
        <Button type="button" variant="secondary" onClick={onDone}>
          Cancel
        </Button>
      </div>
      <ErrorText error={save.error} />
    </form>
  );
}
