import type { Edge } from "@xyflow/react";
import * as THREE from "three";
import { CSS2DObject } from "three/examples/jsm/renderers/CSS2DRenderer.js";
import { serviceState } from "@/features/map/topology";
import { isDatabase } from "@/api";
import type { EdgeKind, MapNode } from "./build";
import { cabinet, cloud, disks, floor, FLOOR, gateway, globe, hub, manager, seed, type Model } from "./models3d";

const edgeColor: Record<EdgeKind, number> = { web: 0x0ea5e9, tunnel: 0xf97316, db: 0x4169e1, net: 0x8b5cf6 };

/** What of a node the scene draws, uptimes and the like left out. */
export function looks(n: MapNode) {
  const d = n.data;
  switch (d.kind) {
    case "service":
      return [d.svc.name, d.svc.kind, d.svc.replicas, serviceState(d.svc), d.svc.containers.filter((c) => !c.retired).length];
    case "project":
      return [d.project.name, d.previews.length];
    case "network":
      return d.net.custom ?? d.net.name;
    case "server":
      return d.server.name;
    default:
      return d.kind;
  }
}

/** Adds the scene's objects to root; returns their bounds. */
export function build(root: THREE.Group, nodes: MapNode[], edges: Edge[], dark: boolean): THREE.Box3 {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const abs = (n: MapNode): { x: number; y: number } => {
    const p = n.parentId ? byId.get(n.parentId) : undefined;
    const o = p ? abs(p) : { x: 0, y: 0 };
    return { x: o.x + n.position.x, y: o.y + n.position.y };
  };
  // Where lines attach: the top of each object, at its centre.
  const tops = new Map<string, THREE.Vector3>();

  for (const n of nodes) {
    const { x, y } = abs(n);
    const w = n.width ?? 0;
    const d = n.height ?? 0;
    const g = new THREE.Group();
    g.userData.id = n.id;
    g.position.set(x + w / 2, 0, y + d / 2);
    const data = n.data;
    let model: Model;
    let label = "";
    switch (data.kind) {
      case "server":
        model = { obj: floor(w, d, 3, dark), top: 0 };
        model.obj.position.y = -3;
        label = data.server.name;
        break;
      case "project": {
        const o = new THREE.Group();
        // Its previews: dimmer floors under it, a step down and to the back right.
        for (let k = Math.min(data.previews.length, 2); k >= 1; k--) {
          const f = floor(w, d, FLOOR, dark, true);
          f.position.set(10 * k, -9 * k, 10 * k);
          o.add(f);
        }
        o.add(floor(w, d, FLOOR, dark));
        model = { obj: o, top: FLOOR };
        label = data.previews.length ? `${data.project.name} · ${data.previews.length} preview${data.previews.length === 1 ? "" : "s"}` : data.project.name;
        break;
      }
      case "service": {
        const { svc } = data;
        const state = serviceState(svc);
        const live = svc.containers.filter((c) => !c.retired).length;
        model = isDatabase(svc.kind) ? disks(Math.min(w, d) * 0.32, svc.kind, state, live > 0) : cabinet(w, d, Math.max(1, svc.replicas), live, state, n.id);
        model.obj.position.y += FLOOR;
        model.top += FLOOR;
        label = svc.name;
        break;
      }
      case "network":
        model = hub(Math.min(w, d) * 0.45);
        label = data.net.custom ?? data.net.name;
        break;
      case "internet":
        model = globe(Math.min(w, d) * 0.36);
        label = "Internet";
        break;
      case "traefik":
        model = gateway(w, d, n.id);
        label = "Traefik";
        break;
      case "tunnel":
        model = cloud(w, d);
        label = "Cloudflare tunnel";
        break;
      case "manager":
        model = manager(w, d, dark);
        label = "kipitiny";
        break;
    }
    g.add(model.obj);
    if (label) {
      const frame = data.kind === "project" || data.kind === "server";
      const el = document.createElement("div");
      el.className = frame
        ? "whitespace-nowrap select-none text-sm font-medium text-foreground"
        : "whitespace-nowrap select-none rounded-md border bg-card/90 px-1.5 py-0.5 text-[11px] font-medium text-card-foreground shadow-xs";
      el.textContent = label;
      const o = new CSS2DObject(el);
      // Frames are named above their back corner (clear of what stands on them), the rest above their top.
      o.position.set(frame ? -w / 2 : 0, model.top + (frame ? 4 : 14), frame ? -d / 2 : 0);
      o.center.set(frame ? 0 : 0.5, 1);
      g.add(o);
    }
    root.add(g);
    tops.set(n.id, new THREE.Vector3(g.position.x, model.top, g.position.z));
  }

  const curves = new Map<string, THREE.Curve<THREE.Vector3>>();
  for (const e of edges) {
    const a = tops.get(e.source);
    const b = tops.get(e.target);
    if (!a || !b) continue;
    const kind = (e.data as { kind: EdgeKind }).kind;
    const lift = Math.max(a.y, b.y) + 40 + a.distanceTo(b) * 0.15;
    const curve = new THREE.QuadraticBezierCurve3(a, new THREE.Vector3((a.x + b.x) / 2, lift, (a.z + b.z) / 2), b);
    curves.set(e.id, curve);
    const tubeMat = new THREE.MeshBasicMaterial({ color: edgeColor[kind], transparent: true, opacity: 0.85 });
    if (kind === "db" || kind === "net") {
      // Dashed: every other piece of the curve.
      const pts = curve.getSpacedPoints(kind === "db" ? 40 : 60);
      for (let i = 0; i + 1 < pts.length; i += 2) root.add(new THREE.Mesh(new THREE.TubeGeometry(new THREE.LineCurve3(pts[i], pts[i + 1]), 1, 1.3, 6), tubeMat));
    } else {
      root.add(new THREE.Mesh(new THREE.TubeGeometry(curve, 48, 1.3, 6), tubeMat));
    }
    // Queries going to each database and back.
    if (kind === "db") root.add(packet(curve, e.id, kind, true));
  }

  // Requests ride whole routes, Internet to app: one packet per line that
  // ends the way in (into an app or the manager), along the lines before it.
  const ingress = edges.filter((e) => curves.has(e.id) && (e.data as { kind: EdgeKind }).kind !== "db" && (e.data as { kind: EdgeKind }).kind !== "net");
  const into = new Map(ingress.map((e) => [e.target, e]));
  const route = (e: Edge): THREE.Curve<THREE.Vector3>[] => [...(into.has(e.source) ? route(into.get(e.source)!) : []), curves.get(e.id)!];
  for (const e of ingress) {
    if (ingress.some((next) => next.source === e.target)) continue;
    const path = new THREE.CurvePath<THREE.Vector3>();
    for (const c of route(e)) path.add(c);
    root.add(packet(path, e.id, "web", false));
  }

  root.traverse((o) => {
    if (o instanceof THREE.Mesh && !(o.material instanceof THREE.MeshBasicMaterial)) o.castShadow = o.receiveShadow = true;
  });
  return new THREE.Box3().setFromObject(root);
}

/** A glowing packet along a path, at a steady speed; back and forth for a query. */
function packet(path: THREE.Curve<THREE.Vector3>, id: string, kind: EdgeKind, roundTrip: boolean) {
  const p = new THREE.Mesh(new THREE.SphereGeometry(roundTrip ? 4 : 5.5, 16, 16), new THREE.MeshBasicMaterial({ color: roundTrip ? 0x93c5fd : 0xe0f2fe }));
  p.add(new THREE.Mesh(new THREE.SphereGeometry(roundTrip ? 7 : 9, 16, 16), new THREE.MeshBasicMaterial({ color: edgeColor[kind], transparent: true, opacity: 0.35 })));
  // About 220 units a second, whatever the length.
  const period = Math.max(1.2, (path.getLength() * (roundTrip ? 2 : 1)) / 220);
  const phase = seed(id);
  p.userData.tick = (t: number) => {
    const k = (t / period + phase) % 1;
    p.position.copy(path.getPointAt(roundTrip ? 1 - Math.abs(1 - 2 * k) : k));
  };
  return p;
}
