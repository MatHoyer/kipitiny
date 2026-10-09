import { useQuery } from "@tanstack/react-query";
import { lazy, Suspense } from "react";
import { ErrorText, Loading } from "@/components/common";
import { PageHeader } from "@/components/page-header";
import { api } from "../api";

const MapCanvas = lazy(() => import("@/components/canvas/map-canvas"));

/** Every server on one canvas: ingress, Traefik, projects and their services, networks. */
export function MapPage() {
  const topology = useQuery({ queryKey: ["topology"], queryFn: () => api.topology(), refetchInterval: 5_000 });

  return (
    <>
      <PageHeader crumbs={[{ label: "Map" }]} />
      <div className="flex min-h-0 flex-1 flex-col p-3 sm:p-4">
        {topology.error ? (
          <ErrorText error={topology.error} />
        ) : !topology.data ? (
          <Loading />
        ) : (
          <Suspense fallback={<Loading />}>
            <MapCanvas servers={topology.data.servers} className="h-[calc(100dvh-7rem)] min-h-[28rem]" />
          </Suspense>
        )}
      </div>
    </>
  );
}
