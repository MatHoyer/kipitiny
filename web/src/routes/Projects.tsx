import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, ChevronRight, FolderKanban, Globe, Layers, Plus, TriangleAlert } from "lucide-react";
import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router";
import { ServiceIcon } from "@/components/service-icon";
import { EmptyState, ErrorText, IconTile, StatCard, Tag, Loading } from "@/components/common";
import { PageBody, PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
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
import { memoryOf, useUsage } from "@/components/usage";
import { formatBytes, liveState, timeAgo, troubled } from "@/lib/format";
import { cn } from "@/lib/utils";
import { api, isDatabase, type Project as ProjectT, type Service } from "../api";

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
          <Loading />
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
              <StatCard icon={Layers} label="Services" value={all.length} hint={`${all.filter((s) => isDatabase(s.kind)).length} databases`} />
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
  const states = services?.map(liveState) ?? [];
  const bad = states.filter(troubled).length;
  const memory = memoryOf(useUsage().data, (u) => u.projectId === p.id);

  return (
    <Link to={`/projects/${p.id}`} className="group rounded-xl outline-none focus-visible:ring-3 focus-visible:ring-ring/50">
      <Card className="h-full gap-3 px-4 transition-all group-hover:-translate-y-px group-hover:shadow-md group-hover:ring-foreground/20">
        <div className="flex items-start gap-3">
          <IconTile icon={FolderKanban} />
          <div className="min-w-0 flex-1">
            <p className="truncate font-medium">{p.name}</p>
            <p className="text-xs text-muted-foreground">
              {services ? `${services.length} service${services.length === 1 ? "" : "s"}` : <Spinner className="inline size-3" />}
              {server && ` · ${server}`}
            </p>
          </div>
          <ChevronRight className="size-4 text-muted-foreground transition-transform group-hover:translate-x-0.5" />
        </div>
        {services && services.length > 0 && (
          <div className="flex flex-wrap items-center gap-1.5" aria-label="Service states">
            {services.map((s, i) => (
              <span
                key={s.id}
                role="img"
                aria-label={`${s.name}: ${states[i]}`}
                title={`${s.name}: ${states[i]}`}
                className="relative flex size-6 items-center justify-center rounded-md bg-muted"
              >
                <ServiceIcon service={s} className="size-3.5 text-muted-foreground" />
                <span className={cn("absolute -right-0.5 -bottom-0.5 size-2 rounded-full ring-2 ring-card", stateDot(states[i]))} />
              </span>
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
        <p className="mt-auto text-xs text-muted-foreground">
          {memory ? `${formatBytes(memory)} memory · ` : ""}Updated {timeAgo(p.updatedAt)}
        </p>
      </Card>
    </Link>
  );
}

const stateDot = (state: string) =>
  state === "running" ? "bg-emerald-500" : troubled(state) ? "bg-red-500" : state === "starting" ? "animate-pulse bg-amber-500" : "bg-muted-foreground/40";

/**
 * Creates a project. With open and onOpenChange, the caller opens it (no
 * button); stay keeps the user where they are (the map) instead of opening it.
 */
export function NewProjectDialog({ stay, ...controlled }: { stay?: boolean; open?: boolean; onOpenChange?: (open: boolean) => void } = {}) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const servers = useQuery({ queryKey: ["servers"], queryFn: api.servers });
  const [ownOpen, setOwnOpen] = useState(false);
  const open = controlled.onOpenChange ? !!controlled.open : ownOpen;
  const setOpen = controlled.onOpenChange ?? setOwnOpen;
  const [name, setName] = useState("");
  const [serverId, setServerId] = useState("local");
  const create = useMutation({
    meta: { error: "Couldn't create the project" },
    mutationFn: () => api.createProject(name.trim(), serverId),
    onSuccess: (p) => {
      qc.invalidateQueries({ queryKey: ["projects"] });
      setOpen(false);
      setName("");
      qc.invalidateQueries({ queryKey: ["topology"] });
      if (!stay) navigate(`/projects/${p.id}`);
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
      {!controlled.onOpenChange && (
        <DialogTrigger asChild>
          <Button size="sm">
            <Plus data-icon="inline-start" />
            New project
          </Button>
        </DialogTrigger>
      )}
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
