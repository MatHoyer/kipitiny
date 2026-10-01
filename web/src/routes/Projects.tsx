import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, ChevronRight, FolderKanban, Globe, Layers, Plus, TriangleAlert } from "lucide-react";
import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router";
import { PostgresIcon } from "@/components/brand-icons";
import { Empty, EmptyState, ErrorText, IconTile, StatCard, Tag } from "@/components/common";
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
import { liveState, timeAgo, troubled } from "@/lib/format";
import { cn } from "@/lib/utils";
import { api, type Project as ProjectT, type Service } from "../api";

export function Projects() {
  const projects = useQuery({ queryKey: ["projects"], queryFn: api.projects });
  const servers = useQuery({ queryKey: ["servers"], queryFn: api.servers });
  // Same key as the project page, so the cache is shared.
  const services = useQueries({
    queries: (projects.data ?? []).map((p) => ({
      queryKey: ["services", p.id],
      queryFn: () => api.services(p.id),
      refetchInterval: 15_000,
    })),
  });
  const serverName = (id: string) => servers.data?.find((s) => s.id === id)?.name ?? id;
  const multi = (servers.data?.length ?? 0) > 1;
  const all = services.flatMap((q) => q.data ?? []);
  const states = all.map(liveState);

  return (
    <>
      <PageHeader crumbs={[{ label: "Projects" }]} actions={<NewProjectDialog />} />
      <PageBody>
        {projects.isPending ? (
          <Empty>Loading…</Empty>
        ) : projects.error ? (
          <ErrorText error={projects.error} />
        ) : projects.data.length === 0 ? (
          <EmptyState
            icon={FolderKanban}
            title="No projects yet"
            description="A project groups apps and databases on one private network."
            action={<NewProjectDialog />}
          />
        ) : (
          <>
            <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
              <StatCard icon={FolderKanban} label="Projects" value={projects.data.length} />
              <StatCard icon={Layers} label="Services" value={all.length} hint={`${all.filter((s) => s.kind === "postgres").length} databases`} />
              <StatCard icon={Activity} label="Running" value={states.filter((s) => s === "running").length} tone="good" />
              <StatCard
                icon={TriangleAlert}
                label="Need attention"
                value={states.filter(troubled).length}
                tone={states.some(troubled) ? "bad" : undefined}
              />
            </div>
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {projects.data.map((p, i) => (
                <ProjectCard key={p.id} project={p} services={services[i]?.data} server={multi ? serverName(p.serverId) : undefined} />
              ))}
            </div>
          </>
        )}
      </PageBody>
    </>
  );
}

function ProjectCard({ project: p, services, server }: { project: ProjectT; services?: Service[]; server?: string }) {
  const domains = services?.filter((s) => s.domain).map((s) => s.domain) ?? [];
  const hasDb = services?.some((s) => s.kind === "postgres");
  const states = services?.map(liveState) ?? [];
  const bad = states.filter(troubled).length;

  return (
    <Link to={`/projects/${p.id}`} className="group rounded-xl outline-none focus-visible:ring-3 focus-visible:ring-ring/50">
      <Card className="h-full gap-3 px-4 transition-all group-hover:-translate-y-px group-hover:shadow-md group-hover:ring-foreground/20">
        <div className="flex items-start gap-3">
          <IconTile icon={FolderKanban} />
          <div className="min-w-0 flex-1">
            <p className="flex items-center gap-2 font-medium">
              <span className="truncate">{p.name}</span>
              {hasDb && <PostgresIcon aria-label="Has a database" className="size-3.5 shrink-0 text-[#4169E1]" />}
            </p>
            <p className="text-xs text-muted-foreground">
              {services ? `${services.length} service${services.length === 1 ? "" : "s"}` : "…"}
              {server && ` · ${server}`}
            </p>
          </div>
          <ChevronRight className="size-4 text-muted-foreground transition-transform group-hover:translate-x-0.5" />
        </div>
        {services && services.length > 0 && (
          <div className="flex flex-wrap items-center gap-1" aria-label="Service states">
            {services.map((s, i) => (
              <span key={s.id} title={`${s.name}: ${states[i]}`} className={cn("size-2 rounded-full", stateDot(states[i]))} />
            ))}
            <span className="ml-1 text-xs text-muted-foreground">
              {bad ? `${bad} need${bad === 1 ? "s" : ""} attention` : states.every((s) => s === "running") ? "all running" : ""}
            </span>
          </div>
        )}
        {domains.length > 0 && (
          <div className="flex flex-wrap gap-1">
            {domains.slice(0, 2).map((d) => (
              <Tag key={d} className="flex max-w-full items-center gap-1 truncate font-mono font-normal">
                <Globe className="size-3 shrink-0" />
                {d}
              </Tag>
            ))}
            {domains.length > 2 && <Tag>+{domains.length - 2}</Tag>}
          </div>
        )}
        <p className="mt-auto text-xs text-muted-foreground">Updated {timeAgo(p.updatedAt)}</p>
      </Card>
    </Link>
  );
}

const stateDot = (state: string) =>
  state === "running" ? "bg-emerald-500" : troubled(state) ? "bg-red-500" : state === "starting" ? "animate-pulse bg-amber-500" : "bg-muted-foreground/40";

function NewProjectDialog() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const servers = useQuery({ queryKey: ["servers"], queryFn: api.servers });
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [serverId, setServerId] = useState("local");
  const create = useMutation({
    meta: { error: "Couldn't create the project" },
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
