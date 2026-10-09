import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Background, Controls, MiniMap, Panel, ReactFlow, ReactFlowProvider, useNodesState, type Node, type OnNodeDrag } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { RotateCcw } from "lucide-react";
import { useTheme } from "next-themes";
import { useEffect, useMemo, useState } from "react";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { api, type CanvasPoint, type ServerTopology } from "@/api";
import { buildGraph, type EdgeKind, type MapNode } from "./build";
import "./canvas.css";
import { nodeTypes } from "./nodes";
import { NodePanel } from "./panel";

/**
 * The map as a canvas: pan, zoom, drag nodes (positions are saved for every
 * user), click one for its details. projectView keeps only what a project's
 * map needs (no manager; ingress only if something is public).
 */
export default function MapCanvas(props: { servers: ServerTopology[]; projectView?: boolean; className?: string }) {
  return (
    <ReactFlowProvider>
      <Flow {...props} />
    </ReactFlowProvider>
  );
}

function Flow({ servers, projectView, className }: { servers: ServerTopology[]; projectView?: boolean; className?: string }) {
  const qc = useQueryClient();
  const { resolvedTheme } = useTheme();
  const layout = useQuery({ queryKey: ["canvas"], queryFn: api.canvasLayout });
  const [nodes, setNodes, onNodesChange] = useNodesState<MapNode>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const saved = layout.data?.positions;

  const save = useMutation({
    meta: { error: "Couldn't save the layout" },
    mutationFn: api.saveCanvasLayout,
    onSuccess: (_, positions) => qc.setQueryData(["canvas"], { positions: { ...saved, ...positions } }),
  });
  const reset = useMutation({
    meta: { error: "Couldn't reset the layout" },
    mutationFn: api.resetCanvasLayout,
    onSuccess: () => {
      qc.setQueryData(["canvas"], { positions: {} });
      setNodes([]);
    },
  });

  // Rebuild on every refresh, keeping nodes where they are on screen.
  const edges = useMemo(() => (saved ? buildGraph(servers, saved, new Map(), { projectView }).edges : []), [servers, saved, projectView]);
  useEffect(() => {
    if (!saved) return;
    setNodes((prev) => {
      const current = new Map(prev.map((n) => [n.id, n.position]));
      const picked = new Set(prev.filter((n) => n.selected).map((n) => n.id));
      return buildGraph(servers, saved, current, { projectView }).nodes.map((n) => ({ ...n, selected: picked.has(n.id) }));
    });
  }, [servers, saved, projectView, setNodes]);

  const onDragStop: OnNodeDrag<MapNode> = (_, __, dragged) => {
    const positions: Record<string, CanvasPoint> = {};
    for (const n of dragged) positions[n.id] = n.position;
    save.mutate(positions);
  };

  const selectedNode = nodes.find((n) => n.id === selected);
  const dark = resolvedTheme === "dark";

  if (!saved) return <div className={cn("rounded-xl border bg-muted/20", className)} />;
  return (
    <div className={cn("relative overflow-hidden rounded-xl border", className)}>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onNodesChange={onNodesChange}
        onNodeDragStop={onDragStop}
        onNodeClick={(_, n) => setSelected(n.id)}
        onPaneClick={() => setSelected(null)}
        nodesConnectable={false}
        colorMode={dark ? "dark" : "light"}
        fitView
        fitViewOptions={{ padding: { top: "64px", right: "32px", bottom: "32px", left: "32px" }, maxZoom: 1 }}
        minZoom={0.1}
        proOptions={{ hideAttribution: true }}
      >
        <Background gap={20} size={1.5} />
        <Controls showInteractive={false} />
        <MiniMap pannable zoomable nodeStrokeWidth={2} nodeColor={(n: Node) => (n.type === "project" || n.type === "server" ? "transparent" : "var(--muted-foreground)")} />
        <Panel position="top-left" className="flex flex-wrap items-center gap-3 rounded-lg border bg-card/90 px-3 py-1.5 backdrop-blur">
          <Legend />
          {Object.keys(saved).length > 0 && (
            <ConfirmDialog
              trigger={
                <Button variant="ghost" size="sm" className="h-6 px-1.5 text-xs text-muted-foreground">
                  <RotateCcw data-icon="inline-start" />
                  Reset layout
                </Button>
              }
              title="Reset the layout?"
              description="Every node goes back to its automatic place, for everyone."
              confirmLabel="Reset"
              onConfirm={() => reset.mutate()}
            />
          )}
        </Panel>
      </ReactFlow>
      {selectedNode && <NodePanel node={selectedNode} servers={servers} onClose={() => setSelected(null)} />}
    </div>
  );
}

const legend: [EdgeKind, string][] = [
  ["web", "HTTP routing"],
  ["tunnel", "Cloudflare tunnel"],
  ["db", "Uses database"],
  ["net", "On a network"],
];

function Legend() {
  return (
    <ul className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
      {legend.map(([kind, label]) => (
        <li key={kind} className="flex items-center gap-1.5">
          <svg aria-hidden width="20" height="6" className={`map-legend-${kind}`}>
            <path d="M0,3 H20" strokeWidth={2} />
          </svg>
          {label}
        </li>
      ))}
    </ul>
  );
}
