import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { api } from "../api";
import { Button, Card, ErrorText, Input, Select } from "../ui";

export function Projects() {
  const qc = useQueryClient();
  const projects = useQuery({ queryKey: ["projects"], queryFn: api.projects });
  const servers = useQuery({ queryKey: ["servers"], queryFn: api.servers });
  const [name, setName] = useState("");
  const [serverId, setServerId] = useState("local");
  const serverName = (id: string) => servers.data?.find((s) => s.id === id)?.name ?? id;
  const multi = (servers.data?.length ?? 0) > 1;

  const create = useMutation({
    mutationFn: (n: string) => api.createProject(n, serverId),
    onSuccess: () => {
      setName("");
      qc.invalidateQueries({ queryKey: ["projects"] });
    },
  });

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (name.trim()) create.mutate(name.trim());
  };

  return (
    <div className="space-y-6">
      <h1 className="text-xl font-semibold">Projects</h1>

      <form onSubmit={onSubmit} className="flex max-w-xl gap-2">
        <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="my-project" />
        {multi && (
          <Select value={serverId} onChange={(e) => setServerId(e.target.value)} className="!w-48">
            {servers.data?.map((s) => (
              <option key={s.id} value={s.id}>
                on {s.name}
              </option>
            ))}
          </Select>
        )}
        <Button disabled={create.isPending}>Create</Button>
      </form>
      <ErrorText error={create.error} />

      {projects.isPending ? (
        <p className="text-sm text-zinc-500">Loading…</p>
      ) : projects.error ? (
        <ErrorText error={projects.error} />
      ) : projects.data.length === 0 ? (
        <p className="text-sm text-zinc-500">No projects yet.</p>
      ) : (
        <Card>
          <ul className="-my-2 divide-y divide-zinc-200 dark:divide-zinc-800">
            {projects.data.map((p) => (
              <li key={p.id}>
                <Link to={`/projects/${p.id}`} className="flex justify-between py-2 text-sm hover:underline">
                  <span className="font-medium">{p.name}</span>
                  {multi && <span className="text-xs text-zinc-500">{serverName(p.serverId)}</span>}
                </Link>
              </li>
            ))}
          </ul>
        </Card>
      )}
    </div>
  );
}
