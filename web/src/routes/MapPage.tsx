import { useQuery } from "@tanstack/react-query";
import { Search, TriangleAlert } from "lucide-react";
import { useState } from "react";
import { ErrorText, Loading } from "@/components/common";
import { PageBody, PageHeader } from "@/components/page-header";
import { MapLegend, projectProblems, ServerCard } from "@/components/topology";
import { Input } from "@/components/ui/input";
import { Toggle } from "@/components/ui/toggle";
import { api, type TopoProject } from "../api";

/** Below this many projects the map needs no filter. */
const FILTER_FROM = 8;

/** Every server: ingress, Traefik, the proxy network and a card per project. */
export function MapPage() {
  const topology = useQuery({ queryKey: ["topology"], queryFn: () => api.topology(), refetchInterval: 5_000 });
  const [q, setQ] = useState("");
  const [problemsOnly, setProblemsOnly] = useState(false);

  const all = topology.data?.servers ?? [];
  const needle = q.trim().toLowerCase();
  const filtering = needle !== "" || problemsOnly;
  const matches = (p: TopoProject) =>
    (!problemsOnly || projectProblems(p) > 0) &&
    (!needle ||
      [p.name, p.network, ...p.services.flatMap((s) => [s.name, s.domain ?? ""])].some((v) => v.toLowerCase().includes(needle)));
  const servers = filtering
    ? all.map((s) => ({ ...s, projects: s.projects.filter(matches) })).filter((s) => s.projects.length > 0)
    : all;
  const projectCount = all.reduce((n, s) => n + s.projects.length, 0);

  return (
    <>
      <PageHeader crumbs={[{ label: "Map" }]} />
      <PageBody>
        {topology.error ? (
          <ErrorText error={topology.error} />
        ) : !topology.data ? (
          <Loading />
        ) : (
          <>
            {(projectCount >= FILTER_FROM || filtering) && (
              <div className="flex flex-wrap items-center gap-2">
                <div className="relative min-w-48 flex-1">
                  <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
                  <Input
                    value={q}
                    onChange={(e) => setQ(e.target.value)}
                    placeholder="Search projects, services and domains"
                    aria-label="Search"
                    className="pl-9"
                  />
                </div>
                <Toggle variant="outline" pressed={problemsOnly} onPressedChange={setProblemsOnly}>
                  <TriangleAlert />
                  Problems only
                </Toggle>
              </div>
            )}
            <MapLegend compact />
            {servers.length === 0 ? (
              <p className="py-12 text-center text-sm text-muted-foreground">No projects match.</p>
            ) : (
              servers.map((s) => <ServerCard key={s.id} server={s} showName={all.length > 1} compact />)
            )}
          </>
        )}
      </PageBody>
    </>
  );
}
