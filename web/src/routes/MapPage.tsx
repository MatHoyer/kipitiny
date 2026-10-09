import { useQuery } from "@tanstack/react-query";
import { Search, TriangleAlert } from "lucide-react";
import { lazy, Suspense, useState } from "react";
import { ErrorText, Loading } from "@/components/common";
import { PageHeader } from "@/components/page-header";
import { projectProblems } from "@/components/topology";
import { Input } from "@/components/ui/input";
import { Toggle } from "@/components/ui/toggle";
import { api, type TopoProject } from "../api";

const MapCanvas = lazy(() => import("@/components/canvas/map-canvas"));

/** Every server on one canvas: ingress, Traefik, projects and their services, networks. */
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
  const servers = filtering ? all.map((s) => ({ ...s, projects: s.projects.filter(matches) })) : all;

  return (
    <>
      <PageHeader
        crumbs={[{ label: "Map" }]}
        actions={
          topology.data && (
            <div className="flex items-center gap-2">
              <div className="relative">
                <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search" aria-label="Search" className="h-8 w-44 pl-9 sm:w-60" />
              </div>
              <Toggle size="sm" variant="outline" pressed={problemsOnly} onPressedChange={setProblemsOnly} aria-label="Problems only">
                <TriangleAlert />
                <span className="max-sm:hidden">Problems only</span>
              </Toggle>
            </div>
          )
        }
      />
      <div className="flex min-h-0 flex-1 flex-col p-3 sm:p-4">
        {topology.error ? (
          <ErrorText error={topology.error} />
        ) : !topology.data ? (
          <Loading />
        ) : (
          <Suspense fallback={<Loading />}>
            <MapCanvas servers={servers} className="h-[calc(100dvh-7rem)] min-h-[28rem]" />
          </Suspense>
        )}
      </div>
    </>
  );
}
