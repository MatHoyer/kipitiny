import type { Edge, Node } from "@xyflow/react";
import { isDatabase, type CanvasPoint, type ServerTopology, type TopoNetwork, type TopoProject, type TopoService } from "@/api";

/*
 * Turns the topology into React Flow nodes and edges. Node IDs are the keys
 * the manager stores positions under ("svc:<id>", "project:<id>"…); a
 * position is relative to the node's parent frame. Nodes nobody moved get a
 * simple layout: Traefik and ingress on top, projects in a grid (apps over
 * databases), networks created by hand below.
 */

export const SVC_W = 240;
export const SVC_H = 112;
const INFRA_W = 240;
const INFRA_H = 84;
const NET_W = 200;
const NET_H = 64;
const GAP = 32;
const PAD = 24;
/** Room for a frame's title. */
const HEAD = 48;
const ROW_W = 1800;
const EMPTY_W = 280;
const EMPTY_H = 120;

export type ServiceData = { kind: "service"; svc: TopoService; project: TopoProject; serverId: string };
export type ProjectData = { kind: "project"; project: TopoProject; net?: TopoNetwork; serverId: string };
export type ServerData = { kind: "server"; server: ServerTopology };
export type InfraData = { kind: "internet" | "tunnel" | "traefik" | "manager"; server: ServerTopology };
export type NetworkData = { kind: "network"; net: TopoNetwork; serverId: string; members: number };
export type MapNodeData = ServiceData | ProjectData | ServerData | InfraData | NetworkData;
export type MapNode = Node<MapNodeData>;

export type EdgeKind = "web" | "tunnel" | "db" | "net";

type Size = { w: number; h: number };

/** Where a node goes: where it is now, else where it was saved, else auto. */
type Placer = (id: string, auto: CanvasPoint) => CanvasPoint;

export function buildGraph(
  servers: ServerTopology[],
  saved: Record<string, CanvasPoint>,
  current: Map<string, CanvasPoint>,
  opts: { projectView?: boolean } = {},
): { nodes: MapNode[]; edges: Edge[] } {
  const place: Placer = (id, auto) => current.get(id) ?? saved[id] ?? auto;
  const nodes: MapNode[] = [];
  const edges: Edge[] = [];
  const framed = servers.length > 1;
  let serverY = 0;

  for (const s of servers) {
    const frameId = `server:${s.id}`;
    const parent = framed ? frameId : undefined;
    const inner: MapNode[] = [];
    let y = framed ? HEAD : 0;
    const x0 = framed ? PAD : 0;
    const add = (n: MapNode, size: Size) => {
      inner.push({ ...n, parentId: parent, extent: parent ? "parent" : undefined, expandParent: !!parent, width: size.w, height: size.h });
    };

    // Ingress: Internet → (tunnel →) Traefik, and the manager's own domain.
    const projectsPublic = s.projects.some((p) => p.services.some((svc) => svc.domain && svc.port));
    if (s.traefik && (!opts.projectView || projectsPublic)) {
      add(infra("internet", s, place(`internet:${s.id}`, { x: x0, y })), { w: INFRA_W, h: INFRA_H });
      if (s.tunnel) {
        add(infra("tunnel", s, place(`tunnel:${s.id}`, { x: x0 + INFRA_W + GAP, y })), { w: INFRA_W, h: INFRA_H });
        edges.push(edge(`internet:${s.id}`, `tunnel:${s.id}`, "tunnel"), edge(`tunnel:${s.id}`, `traefik:${s.id}`, "tunnel"));
      } else {
        edges.push(edge(`internet:${s.id}`, `traefik:${s.id}`, "web"));
      }
      y += INFRA_H + GAP;
      add(infra("traefik", s, place(`traefik:${s.id}`, { x: x0, y })), { w: INFRA_W, h: INFRA_H + 24 });
      if (s.manager && !opts.projectView) {
        add(infra("manager", s, place(`manager:${s.id}`, { x: x0 + INFRA_W + GAP, y })), { w: INFRA_W, h: INFRA_H });
        if (s.manager.domain) edges.push(edge(`traefik:${s.id}`, `manager:${s.id}`, "web"));
      }
      y += INFRA_H + 24 + GAP * 2;
    }

    // Projects, wrapping into rows.
    const projectNets = new Map(s.networks.filter((n) => n.projectId).map((n) => [n.projectId!, n]));
    let px = x0;
    let rowH = 0;
    for (const p of s.projects) {
      const { frame, children, size } = projectFrame(p, projectNets.get(p.id), s.id, place);
      if (px > x0 && px + size.w > x0 + ROW_W) {
        px = x0;
        y += rowH + GAP;
        rowH = 0;
      }
      add({ ...frame, position: place(frame.id, { x: px, y }) }, size);
      inner.push(...children);
      px += size.w + GAP;
      rowH = Math.max(rowH, size.h);
      for (const svc of p.services) {
        if (svc.domain && svc.port && s.traefik) edges.push(edge(`traefik:${s.id}`, `svc:${svc.id}`, "web"));
        for (const db of svc.uses) edges.push(edge(`svc:${svc.id}`, `svc:${db}`, "db"));
      }
    }
    y += rowH + GAP * 2;

    // Networks created by hand, with an edge from each member shown here.
    const shown = new Set(s.projects.flatMap((p) => p.services.map((svc) => svc.id)));
    let nx = x0;
    for (const n of s.networks.filter((n) => n.customId)) {
      const members = s.projects.flatMap((p) => p.services).filter((svc) => svc.networks.includes(n.customId!));
      if (opts.projectView && members.length === 0) continue;
      const id = `net:${n.customId}`;
      add({ id, type: "network", position: place(id, { x: nx, y }), data: { kind: "network", net: n, serverId: s.id, members: members.length } }, { w: NET_W, h: NET_H });
      nx += NET_W + GAP;
      for (const svc of members) if (shown.has(svc.id)) edges.push(edge(`svc:${svc.id}`, id, "net"));
    }

    if (framed) {
      const size = frameSize(inner.filter((n) => n.parentId === frameId), { w: 600, h: 200 });
      nodes.push({
        id: frameId,
        type: "server",
        position: place(frameId, { x: 0, y: serverY }),
        data: { kind: "server", server: s },
        width: size.w,
        height: size.h,
        selectable: true,
      });
      serverY += size.h + GAP * 3;
    }
    nodes.push(...inner);
  }
  for (const n of nodes) {
    n.deletable = false;
    // Frames move by their title; the rest lets clicks through to the lines.
    if (n.type === "project" || n.type === "server") n.dragHandle = ".frame-handle";
  }
  // React Flow wants parents before their children.
  return { nodes: [...nodes.filter((n) => !n.parentId || n.type === "project"), ...nodes.filter((n) => n.parentId && n.type !== "project")], edges };
}

function infra(kind: InfraData["kind"], s: ServerTopology, position: CanvasPoint): MapNode {
  return { id: `${kind}:${s.id}`, type: "infra", position, data: { kind, server: s } };
}

/** A project's frame and its services: public apps, then the other apps, then databases. */
function projectFrame(p: TopoProject, net: TopoNetwork | undefined, serverId: string, place: Placer) {
  const id = `project:${p.id}`;
  const apps = p.services.filter((s) => !isDatabase(s.kind)).sort((a, b) => Number(!!b.domain) - Number(!!a.domain));
  const dbs = p.services.filter((s) => isDatabase(s.kind));
  const children: MapNode[] = [];
  [apps, dbs]
    .filter((row) => row.length > 0)
    .forEach((row, r) =>
      row.forEach((svc, i) => {
        const nid = `svc:${svc.id}`;
        children.push({
          id: nid,
          type: "service",
          parentId: id,
          extent: "parent",
          expandParent: true,
          position: place(nid, { x: PAD + i * (SVC_W + GAP), y: HEAD + r * (SVC_H + GAP) }),
          data: { kind: "service", svc, project: p, serverId },
          width: SVC_W,
          height: SVC_H,
        });
      }),
    );
  const frame: MapNode = { id, type: "project", position: { x: 0, y: 0 }, data: { kind: "project", project: p, net, serverId } };
  return { frame, children, size: frameSize(children, { w: EMPTY_W, h: EMPTY_H }) };
}

/** Big enough for its children, with padding. */
function frameSize(children: MapNode[], min: Size): Size {
  let w = min.w;
  let h = min.h;
  for (const c of children) {
    w = Math.max(w, c.position.x + (c.width ?? SVC_W) + PAD);
    h = Math.max(h, c.position.y + (c.height ?? SVC_H) + PAD);
  }
  return { w, h };
}

/** Lines of a network or a database reference can be selected and deleted; the others only show traffic. */
function edge(source: string, target: string, kind: EdgeKind): Edge {
  const editable = kind === "db" || kind === "net";
  return { id: `${kind}:${source}:${target}`, source, target, type: "smoothstep", data: { kind }, selectable: editable, deletable: editable, focusable: editable, animated: kind === "web" || kind === "tunnel", className: `map-edge-${kind}` };
}

/**
 * Fits every frame tightly around its children, on all four sides: React
 * Flow only ever grows a parent, so a frame stretched by a drag (up and left
 * included) would keep its size. A frame whose content moved right or down
 * moves with it, its children shifting back so nothing moves on screen.
 * Returns the nodes and the IDs whose position changed (to save).
 */
export function fitFrames(nodes: MapNode[]): { nodes: MapNode[]; moved: string[] } {
  const out = nodes.map((n) => ({ ...n, position: { ...n.position } }));
  const moved = new Set<string>();
  // Project frames first: a server frame fits around their new size.
  const frames = out.filter((n) => n.type === "project" || n.type === "server").sort((a, b) => (a.type === "project" ? -1 : 1) - (b.type === "project" ? -1 : 1));
  for (const f of frames) {
    const children = out.filter((n) => n.parentId === f.id);
    const min = f.type === "server" ? { w: 600, h: 200 } : { w: EMPTY_W, h: EMPTY_H };
    if (children.length === 0) {
      f.width = min.w;
      f.height = min.h;
      continue;
    }
    const x1 = Math.min(...children.map((c) => c.position.x));
    const y1 = Math.min(...children.map((c) => c.position.y));
    const x2 = Math.max(...children.map((c) => c.position.x + (c.width ?? SVC_W)));
    const y2 = Math.max(...children.map((c) => c.position.y + (c.height ?? SVC_H)));
    const dx = Math.round(x1 - PAD);
    const dy = Math.round(y1 - HEAD);
    if (dx !== 0 || dy !== 0) {
      f.position = { x: f.position.x + dx, y: f.position.y + dy };
      moved.add(f.id);
      for (const c of children) {
        c.position = { x: c.position.x - dx, y: c.position.y - dy };
        moved.add(c.id);
      }
    }
    f.width = Math.max(min.w, Math.round(x2 - x1) + PAD * 2);
    f.height = Math.max(min.h, Math.round(y2 - y1) + HEAD + PAD);
  }
  return { nodes: out, moved: [...moved] };
}

