import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, Plus } from "lucide-react";
import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router";
import { Empty, ErrorText } from "@/components/common";
import { PageBody, PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingSelect } from "@/components/ui/floating-select";
import { api } from "../api";

export function Projects() {
  const projects = useQuery({ queryKey: ["projects"], queryFn: api.projects });
  const servers = useQuery({ queryKey: ["servers"], queryFn: api.servers });
  const serverName = (id: string) => servers.data?.find((s) => s.id === id)?.name ?? id;
  const multi = (servers.data?.length ?? 0) > 1;

  return (
    <>
      <PageHeader crumbs={[{ label: "Projects" }]} actions={<NewProjectDialog />} />
      <PageBody>
        {projects.isPending ? (
          <Empty>Loading…</Empty>
        ) : projects.error ? (
          <ErrorText error={projects.error} />
        ) : projects.data.length === 0 ? (
          <Card className="items-center gap-2 py-10 text-center">
            <p className="font-medium">No projects yet</p>
            <p className="text-sm text-muted-foreground">A project groups apps and databases on one private network.</p>
          </Card>
        ) : (
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {projects.data.map((p) => (
              <Link key={p.id} to={`/projects/${p.id}`} className="group rounded-xl outline-none focus-visible:ring-3 focus-visible:ring-ring/50">
                <Card className="flex-row items-center px-4 transition-colors group-hover:bg-muted/50">
                  <div className="min-w-0 flex-1">
                    <p className="truncate font-medium">{p.name}</p>
                    {multi && <p className="text-xs text-muted-foreground">{serverName(p.serverId)}</p>}
                  </div>
                  <ChevronRight className="size-4 text-muted-foreground" />
                </Card>
              </Link>
            ))}
          </div>
        )}
      </PageBody>
    </>
  );
}

function NewProjectDialog() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const servers = useQuery({ queryKey: ["servers"], queryFn: api.servers });
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [serverId, setServerId] = useState("local");
  const create = useMutation({
    mutationFn: () => api.createProject(name.trim(), serverId),
    onSuccess: (p) => {
      qc.invalidateQueries({ queryKey: ["projects"] });
      setOpen(false);
      setName("");
      navigate(`/projects/${p.id}`);
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (name.trim()) create.mutate();
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (!o) create.reset();
      }}
    >
      <DialogTrigger asChild>
        <Button size="sm">
          <Plus data-icon="inline-start" />
          New project
        </Button>
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={onSubmit} className="contents">
          <DialogHeader>
            <DialogTitle>New project</DialogTitle>
            <DialogDescription>Its services share a private network.</DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <FloatingInput label="Name" required autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="my-project" />
            {(servers.data?.length ?? 0) > 1 && (
              <FloatingSelect
                label="Server"
                value={serverId}
                onValueChange={setServerId}
                options={servers.data!.map((s) => ({ value: s.id, label: s.name }))}
              />
            )}
            <ErrorText error={create.error} />
          </div>
          <DialogFooter>
            <Button type="submit" disabled={create.isPending}>
              Create
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
