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
/** members are the node IDs of its services. */
export type NetworkData = { kind: "network"; net: TopoNetwork; serverId: string; members: string[] };
export type MapNodeData = ServiceData | ProjectData | ServerData | InfraData | NetworkData;
export type MapNode = Node<MapNodeData>;

export type EdgeKind = "web" | "tunnel" | "db";

type Size = { w: number; h: number };

/** Where a node goes: where it is now, else where it was saved, else auto. */
type Placer = (id: string, auto: CanvasPoint) => CanvasPoint;

export function buildGraph(
  servers: ServerTopology[],
  saved: Record<string, CanvasPoint>,
  current: Map<string, CanvasPoint>,
  opts: { projectView?: boolean } = {},
): { nodes: MapNode[]; edges: Edge[]; areas: NetworkData[] } {
  const place: Placer = (id, auto) => current.get(id) ?? saved[id] ?? auto;
  const nodes: MapNode[] = [];
  const edges: Edge[] = [];
  const areas: NetworkData[] = [];
  const framed = servers.length > 1;
  let serverY = 0;

  for (const s of servers) {
    const frameId = `server:${s.id}`;
    const parent = framed ? frameId : undefined;
    const inner: MapNode[] = [];
    let y = framed ? HEAD : 0;
    const x0 = framed ? PAD : 0;
    const add = (n: MapNode, size: Size) => {
      inner.push({ ...n, parentId: parent, extent: parent ? "parent" : undefined, width: size.w, height: size.h });
    };

    // Ingress: Internet → (tunnel →) Traefik, and the manager's own domain.
    const projectsPublic = s.projects.some((p) => p.services.some((svc) => svc.domain));
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
        if (svc.domain && s.traefik) edges.push(edge(`traefik:${s.id}`, `svc:${svc.id}`, "web"));
        for (const db of svc.uses) edges.push(edge(`svc:${svc.id}`, `svc:${db}`, "db"));
      }
    }
    y += rowH + GAP * 2;

    // Networks created by hand: an area around their services (drawn from
    // where those are on screen), or a node of their own while empty.
    let nx = x0;
    for (const n of s.networks.filter((n) => n.customId)) {
      const members = s.projects.flatMap((p) => p.services).filter((svc) => svc.networks.includes(n.customId!));
      const id = `net:${n.customId}`;
      const data: NetworkData = { kind: "network", net: n, serverId: s.id, members: members.map((svc) => `svc:${svc.id}`) };
      if (members.length > 0) {
        areas.push(data);
        continue;
      }
      if (opts.projectView) continue;
      add({ id, type: "network", position: place(id, { x: nx, y }), data }, { w: NET_W, h: NET_H });
      nx += NET_W + GAP;
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
  return { nodes: [...nodes.filter((n) => !n.parentId || n.type === "project"), ...nodes.filter((n) => n.parentId && n.type !== "project")], edges, areas };
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

/** Lines of a database reference can be selected and deleted; the others only show traffic. */
function edge(source: string, target: string, kind: EdgeKind): Edge {
  const editable = kind === "db";
  return { id: `${kind}:${source}:${target}`, source, target, type: "smoothstep", data: { kind }, selectable: editable, deletable: editable, focusable: editable, animated: kind === "web" || kind === "tunnel", className: `map-edge-${kind}` };
}

/** Space around a network's services; nested networks get a little more each. */
const AREA_PAD = 10;
const AREA_NEST = 10;
/** Room under the services for the network's name. */
const AREA_LABEL = 26;

/**
 * The smallest rectangle around each network's services, from where they are
 * now (so it follows a drag), as nodes drawn under the cards.
 */
export function networkAreas(nodes: MapNode[], areas: NetworkData[]): MapNode[] {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const absolute = (n: MapNode): CanvasPoint => {
    const parent = n.parentId ? byId.get(n.parentId) : undefined;
    if (!parent) return n.position;
    const p = absolute(parent);
    return { x: p.x + n.position.x, y: p.y + n.position.y };
  };
  // Count how many areas each service is already wrapped in, to nest them.
  const depth = new Map<string, number>();
  const out: MapNode[] = [];
  for (const a of areas) {
    const members = a.members.map((id) => byId.get(id)).filter((n): n is MapNode => !!n);
    if (members.length === 0) continue;
    const nest = Math.max(...members.map((m) => depth.get(m.id) ?? 0));
    for (const m of members) depth.set(m.id, nest + 1);
    const pad = AREA_PAD + nest * AREA_NEST;
    let x1 = Infinity, y1 = Infinity, x2 = -Infinity, y2 = -Infinity;
    for (const m of members) {
      const p = absolute(m);
      x1 = Math.min(x1, p.x);
      y1 = Math.min(y1, p.y);
      x2 = Math.max(x2, p.x + (m.width ?? SVC_W));
      y2 = Math.max(y2, p.y + (m.height ?? SVC_H));
    }
    out.push({
      id: `net:${a.net.customId}`,
      type: "networkArea",
      position: { x: x1 - pad, y: y1 - pad },
      width: x2 - x1 + pad * 2,
      height: y2 - y1 + pad * 2 + AREA_LABEL,
      data: a,
      draggable: false,
      deletable: false,
      zIndex: 0,
    });
  }
  return out;
}

