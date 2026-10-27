import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Background, Controls, MiniMap, Panel, ReactFlow, ReactFlowProvider, useEdgesState, useNodesState, useReactFlow, type Edge, type Node, type OnNodeDrag } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { Box, FolderPlus, Network, Plus, RotateCcw } from "lucide-react";
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
import { buildGraph, layoutKey, untangle, type EdgeKind, type MapNode } from "./build";
import { useCanvasEdits } from "./edit";
import "./canvas.css";
import { nodeTypes } from "./nodes";
import { NodePanel } from "./panel";

/**
 * The map as a canvas: pan, zoom, drag nodes (positions are saved for every
 * user), click one for its details. With projectId, it keeps only what that
 * project's map needs (no manager; ingress only if something is public), its
 * frame, ingress and networks placed apart from the global map.
 */
export default function MapCanvas(props: { servers: ServerTopology[]; projectId?: string; className?: string }) {
  return (
    <ReactFlowProvider>
      <Flow {...props} />
    </ReactFlowProvider>
  );
}

function Flow({ servers, projectId, className }: { servers: ServerTopology[]; projectId?: string; className?: string }) {
  const qc = useQueryClient();
  const { resolvedTheme } = useTheme();
  const layout = useQuery({ queryKey: ["canvas"], queryFn: api.canvasLayout });
  const [nodes, setNodes, onNodesChange] = useNodesState<MapNode>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [adding, setAdding] = useState<"project" | "network" | null>(null);
  // Projects whose previews are spread out instead of stacked behind them.
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  // Isometric: the same map tilted, to look at. Dragging and drawing lines are
  // off, as React Flow reads pointer moves flat.
  const [iso, setIso] = useState(false);
  const saved = layout.data?.positions;
  const edits = useCanvasEdits(servers);
  const { fitView } = useReactFlow();

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
    const togglePreviews = (id: string) => {
      setExpanded((prev) => {
        const next = new Set(prev);
        if (!next.delete(id)) next.add(id);
        return next;
      });
      // Once the nodes are rebuilt, show where the previews went.
      setTimeout(() => fitView({ ...fitOptions, duration: 300 }), 50);
    };
    const opts = { projectId, expanded, togglePreviews };
    setNodes((prev) => {
      const current = new Map(prev.map((n) => [n.id, n.position]));
      const picked = new Set(prev.filter((n) => n.selected).map((n) => n.id));
      const built = buildGraph(servers, saved, current, opts).nodes.map((n) => ({ ...n, selected: picked.has(n.id) }));
      // Nodes on screen keep their place, then the saved ones; new ones go around them.
      return untangle(built, (n) => (current.has(n.id) ? 0 : saved[layoutKey(n.id, projectId)] ? 1 : 2)).nodes;
    });
    setEdges((prev) => {
      const picked = new Set(prev.filter((e) => e.selected).map((e) => e.id));
      return buildGraph(servers, saved, new Map(), opts).edges.map((e) => ({ ...e, selected: picked.has(e.id) }));
    });
  }, [servers, saved, projectId, expanded, setNodes, setEdges, fitView]);

  // Save what was dragged, and what it pushed away or refitting its frames moved.
  const onDragStop: OnNodeDrag<MapNode> = (_, __, dragged) => {
    const latest = new Map(dragged.map((n) => [n.id, n]));
    const { nodes: fitted, moved } = untangle(
      nodes.map((n) => latest.get(n.id) ?? n),
      (n) => (latest.has(n.id) ? 0 : 1),
    );
    setNodes(fitted);
    const byId = new Map(fitted.map((n) => [n.id, n]));
    const positions: Record<string, CanvasPoint> = {};
    for (const id of [...dragged.map((n) => n.id), ...moved]) positions[layoutKey(id, projectId)] = byId.get(id)!.position;
    save.mutate(positions);
  };

  const selectedNode = nodes.find((n) => n.id === selected);
  const dark = resolvedTheme === "dark";

  if (!saved) return <div className={cn("rounded-xl border bg-muted/20", className)} />;
  return (
    <div className={cn("relative overflow-hidden rounded-xl border", iso && "map-iso", className)}>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onNodeDragStop={onDragStop}
        nodesDraggable={!iso}
        nodesConnectable={!iso}
        onNodeClick={(_, n) => setSelected(n.id)}
        onPaneClick={() => setSelected(null)}
        isValidConnection={edits.isValidConnection}
        onConnect={edits.onConnect}
        onConnectEnd={edits.onConnectEnd}
        onBeforeDelete={edits.onBeforeDelete}
        deleteKeyCode={["Backspace", "Delete"]}
        connectionLineStyle={{ strokeWidth: 2, strokeDasharray: "4 4" }}
        colorMode={dark ? "dark" : "light"}
        fitView
        fitViewOptions={fitOptions}
        minZoom={0.1}
        proOptions={{ hideAttribution: true }}
      >
        <Background gap={20} size={1.5} />
        <Controls showInteractive={false} />
        <MiniMap className="max-sm:hidden" pannable zoomable nodeStrokeWidth={2} nodeColor={(n: Node) => (n.type === "project" || n.type === "server" ? "transparent" : "var(--muted-foreground)")} />
        <Panel
          position="top-left"
          // On a phone the legend hides; the panel too, unless it holds Reset layout.
          className="flex flex-wrap items-center gap-3 rounded-lg border bg-card/90 px-3 py-1.5 backdrop-blur max-sm:[&:not(:has(>button))]:hidden"
        >
          <div className="max-sm:hidden">
            <Legend />
          </div>
          <Button
            variant={iso ? "secondary" : "ghost"}
            size="sm"
            aria-pressed={iso}
            title={iso ? "Back to the flat map" : "Isometric view (to look at: no dragging)"}
            onClick={() => setIso(!iso)}
            className="h-6 px-1.5 text-xs text-muted-foreground"
          >
            <Box data-icon="inline-start" />
            3D
          </Button>
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
              {!projectId && (
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
          {iso ? "Isometric view: pan, zoom and click nodes · 3D again to drag and edit" : "Drag a service's bottom dot onto a database or network · select a line and press Delete to remove it"}
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

const fitOptions = { padding: { top: "64px", right: "32px", bottom: "32px", left: "32px" }, maxZoom: 1 } as const;

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
