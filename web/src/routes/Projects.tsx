import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { api } from "../api";
import { Button, Card, ErrorText, Input } from "../ui";

export function Projects() {
  const qc = useQueryClient();
  const projects = useQuery({ queryKey: ["projects"], queryFn: api.projects });
  const [name, setName] = useState("");

  const create = useMutation({
    mutationFn: api.createProject,
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

      <form onSubmit={onSubmit} className="flex max-w-md gap-2">
        <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="my-project" />
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
                <Link to={`/projects/${p.id}`} className="block py-2 text-sm font-medium hover:underline">
                  {p.name}
                </Link>
              </li>
            ))}
          </ul>
        </Card>
      )}
    </div>
  );
}
