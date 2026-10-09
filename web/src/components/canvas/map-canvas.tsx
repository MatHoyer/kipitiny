import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Background, Controls, MiniMap, Panel, ReactFlow, ReactFlowProvider, useEdgesState, useNodesState, type Edge, type Node, type OnNodeDrag } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { FolderPlus, Network, Plus, RotateCcw } from "lucide-react";
import { useTheme } from "next-themes";
import { useEffect, useState } from "react";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { NewProjectDialog } from "@/routes/Projects";
import { CreateNetworkForm } from "@/routes/settings/Networks";
import { cn } from "@/lib/utils";
import { api, type CanvasPoint, type ServerTopology } from "@/api";
import { buildGraph, type EdgeKind, type MapNode } from "./build";
import { useCanvasEdits } from "./edit";
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
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [adding, setAdding] = useState<"project" | "network" | null>(null);
  const saved = layout.data?.positions;
  const edits = useCanvasEdits(servers);

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

  // Rebuild on every refresh, keeping nodes where they are on screen and
  // what is selected.
  useEffect(() => {
    if (!saved) return;
    setNodes((prev) => {
      const current = new Map(prev.map((n) => [n.id, n.position]));
      const picked = new Set(prev.filter((n) => n.selected).map((n) => n.id));
      return buildGraph(servers, saved, current, { projectView }).nodes.map((n) => ({ ...n, selected: picked.has(n.id) }));
    });
    setEdges((prev) => {
      const picked = new Set(prev.filter((e) => e.selected).map((e) => e.id));
      return buildGraph(servers, saved, new Map(), { projectView }).edges.map((e) => ({ ...e, selected: picked.has(e.id) }));
    });
  }, [servers, saved, projectView, setNodes, setEdges]);

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
        onEdgesChange={onEdgesChange}
        onNodeDragStop={onDragStop}
        onNodeClick={(_, n) => setSelected(n.id)}
        onPaneClick={() => setSelected(null)}
        isValidConnection={edits.isValidConnection}
        onConnect={edits.onConnect}
        onBeforeDelete={edits.onBeforeDelete}
        deleteKeyCode={["Backspace", "Delete"]}
        connectionLineStyle={{ strokeWidth: 2, strokeDasharray: "4 4" }}
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
        <Panel position="top-right" className={cn(selectedNode && "invisible")}>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button size="sm">
                <Plus data-icon="inline-start" />
                Add
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              {!projectView && (
                <DropdownMenuItem onSelect={() => setAdding("project")}>
                  <FolderPlus />
                  Project
                </DropdownMenuItem>
              )}
              <DropdownMenuItem onSelect={() => setAdding("network")}>
                <Network />
                Network
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
          <NewProjectDialog stay open={adding === "project"} onOpenChange={(o) => !o && setAdding(null)} />
        </Panel>
        <Panel position="bottom-center" className="rounded-md bg-card/80 px-2 py-1 text-[11px] text-muted-foreground backdrop-blur max-sm:hidden">
          Drag a service's bottom dot onto a database or network · select a line and press Delete to remove it
        </Panel>
      </ReactFlow>
      <Dialog open={adding === "network"} onOpenChange={(o) => !o && setAdding(null)}>
        <DialogContent>{adding === "network" && <CreateNetworkForm onDone={() => setAdding(null)} />}</DialogContent>
      </Dialog>
      {edits.dialogs}
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
