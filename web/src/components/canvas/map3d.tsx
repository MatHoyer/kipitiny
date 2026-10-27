import type { Edge } from "@xyflow/react";
import { useEffect, useRef } from "react";
import * as THREE from "three";
import { MapControls } from "three/examples/jsm/controls/MapControls.js";
import { RoomEnvironment } from "three/examples/jsm/environments/RoomEnvironment.js";
import { RoundedBoxGeometry } from "three/examples/jsm/geometries/RoundedBoxGeometry.js";
import { CSS2DObject, CSS2DRenderer } from "three/examples/jsm/renderers/CSS2DRenderer.js";
import { serviceState } from "@/components/topology";
import { isDatabase, type DatabaseKind } from "@/api";
import type { EdgeKind, MapNode } from "./build";

/*
 * The map as an isometric scene, from the same nodes as the flat one: a
 * project is a tiled floor (its previews stacked under it), an app a server
 * cabinet (one blade per replica, LEDs in its state), a database a stack of
 * disks in its engine's color, Traefik a gateway, the tunnel an orange cloud,
 * the manager kipitiny's mark, the Internet a globe. Lines
 * arch between them; packets run along the HTTP ones, and to databases and
 * back. Models are built from primitives and textures drawn on canvases: no
 * asset to download. Its own chunk: three.js is only downloaded when someone
 * opens it.
 */

const stateColor = (state: string) =>
  ({ running: 0x10b981, healthy: 0x10b981, starting: 0xf59e0b, degraded: 0xf59e0b, restarting: 0xf59e0b, asleep: 0x3b82f6 })[state] ??
  (["failed", "unhealthy", "dead", "exited"].includes(state) ? 0xef4444 : 0x9ca3af);
const dbColor: Record<DatabaseKind, number> = { postgres: 0x336791, redis: 0xd82c20, mysql: 0x00758f, mariadb: 0x8a5a44, mongodb: 0x13aa52 };
const edgeColor: Record<EdgeKind, number> = { web: 0x0ea5e9, tunnel: 0xf97316, db: 0x4169e1, net: 0x8b5cf6 };

export default function Map3D({ nodes, edges, dark, selected, onSelect }: { nodes: MapNode[]; edges: Edge[]; dark: boolean; selected: string | null; onSelect: (id: string | null) => void }) {
  const host = useRef<HTMLDivElement>(null);
  const world = useRef<{ root: THREE.Group; fit: (box: THREE.Box3) => void; select: (id: string | null) => void; light: (box: THREE.Box3) => void } | null>(null);
  const fitted = useRef(false);
  const onSelectRef = useRef(onSelect);
  onSelectRef.current = onSelect;

  // Renderer, camera and controls, once.
  useEffect(() => {
    const el = host.current!;
    const renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true });
    renderer.setPixelRatio(window.devicePixelRatio);
    renderer.shadowMap.enabled = true;
    renderer.shadowMap.type = THREE.PCFSoftShadowMap;
    renderer.toneMapping = THREE.ACESFilmicToneMapping;
    el.appendChild(renderer.domElement);
    const labels = new CSS2DRenderer();
    labels.domElement.className = "pointer-events-none absolute inset-0";
    el.appendChild(labels.domElement);

    const scene = new THREE.Scene();
    // A soft studio around the scene, for metal to reflect.
    const pmrem = new THREE.PMREMGenerator(renderer);
    scene.environment = pmrem.fromScene(new RoomEnvironment(), 0.04).texture;
    scene.environmentIntensity = 0.6;
    scene.add(new THREE.HemisphereLight(0xffffff, 0x8888aa, 1.2));
    const sun = new THREE.DirectionalLight(0xffffff, 2);
    sun.castShadow = true;
    sun.shadow.mapSize.set(4096, 4096);
    sun.shadow.bias = -0.0005;
    scene.add(sun, sun.target);
    const root = new THREE.Group();
    const marks = new THREE.Group();
    scene.add(root, marks);

    // Isometric: orthographic, looking down the (1, 1, 1) diagonal.
    const camera = new THREE.OrthographicCamera(-1, 1, 1, -1, -20000, 20000);
    camera.position.set(1000, 1000, 1000);
    const controls = new MapControls(camera, renderer.domElement);
    controls.enableDamping = true;
    controls.maxPolarAngle = Math.PI / 2.4;
    controls.minZoom = 0.05;
    controls.maxZoom = 4;
    controls.zoomToCursor = true;

    const resize = () => {
      const { clientWidth: w, clientHeight: h } = el;
      renderer.setSize(w, h);
      labels.setSize(w, h);
      Object.assign(camera, { left: -w / 2, right: w / 2, top: h / 2, bottom: -h / 2 });
      camera.updateProjectionMatrix();
    };
    const ro = new ResizeObserver(resize);
    ro.observe(el);
    resize();

    const fit = (box: THREE.Box3) => {
      const center = box.getCenter(new THREE.Vector3());
      const size = box.getSize(new THREE.Vector3());
      controls.target.copy(center);
      camera.position.copy(center).add(new THREE.Vector3(1, 1, 1).multiplyScalar(2000));
      // The bounds' diagonal, seen at 45°, spans about (w + d) / √2 across the screen.
      camera.zoom = Math.min(el.clientWidth / ((size.x + size.z) * 0.75), el.clientHeight / ((size.x + size.z) * 0.45 + size.y), 1.2);
      camera.updateProjectionMatrix();
      controls.update();
    };

    // Hover and click by ray; a drag is not a click.
    const ray = new THREE.Raycaster();
    const pick = (e: PointerEvent) => {
      const r = renderer.domElement.getBoundingClientRect();
      ray.setFromCamera(new THREE.Vector2(((e.clientX - r.left) / r.width) * 2 - 1, -((e.clientY - r.top) / r.height) * 2 + 1), camera);
      for (const hit of ray.intersectObjects(root.children, true)) {
        for (let o: THREE.Object3D | null = hit.object; o; o = o.parent) if (o.userData.id) return o.userData.id as string;
      }
      return null;
    };
    let down: { x: number; y: number } | null = null;
    const onDown = (e: PointerEvent) => (down = { x: e.clientX, y: e.clientY });
    const onUp = (e: PointerEvent) => {
      if (down && Math.hypot(e.clientX - down.x, e.clientY - down.y) < 4) onSelectRef.current(pick(e));
      down = null;
    };
    const onMove = (e: PointerEvent) => (renderer.domElement.style.cursor = pick(e) ? "pointer" : "");
    renderer.domElement.addEventListener("pointerdown", onDown);
    renderer.domElement.addEventListener("pointerup", onUp);
    renderer.domElement.addEventListener("pointermove", onMove);

    // The selected object gets a box drawn around it.
    const select = (id: string | null) => {
      dispose(marks);
      marks.clear();
      const g = id && root.children.find((o) => o.userData.id === id);
      if (g) marks.add(new THREE.Box3Helper(new THREE.Box3().setFromObject(g).expandByScalar(4), 0x6366f1));
    };
    // The sun follows the scene, its shadow box covering it.
    const light = (box: THREE.Box3) => {
      const c = box.getCenter(new THREE.Vector3());
      const r = box.getSize(new THREE.Vector3()).length() / 2 + 100;
      sun.target.position.copy(c);
      sun.position.copy(c).add(new THREE.Vector3(-0.5, 1, 0.35).multiplyScalar(r * 2));
      Object.assign(sun.shadow.camera, { left: -r, right: r, top: r, bottom: -r, near: 1, far: r * 5 });
      sun.shadow.camera.updateProjectionMatrix();
    };
    world.current = { root, fit, select, light };

    let frame = 0;
    const clock = new THREE.Clock();
    const loop = () => {
      frame = requestAnimationFrame(loop);
      const t = clock.getElapsedTime();
      root.traverse((o) => o.userData.tick?.(t));
      controls.update();
      renderer.render(scene, camera);
      labels.render(scene, camera);
    };
    loop();

    return () => {
      cancelAnimationFrame(frame);
      ro.disconnect();
      controls.dispose();
      renderer.domElement.removeEventListener("pointerdown", onDown);
      renderer.domElement.removeEventListener("pointerup", onUp);
      renderer.domElement.removeEventListener("pointermove", onMove);
      dispose(root);
      dispose(marks);
      pmrem.dispose();
      scene.environment?.dispose();
      renderer.dispose();
      el.replaceChildren();
      world.current = null;
    };
  }, []);

  // The scene, rebuilt when something it shows changes (the map refreshes
  // every few seconds, container uptimes with it); the view is fitted once.
  const shown = useRef("");
  useEffect(() => {
    const w = world.current;
    const key = JSON.stringify([dark, edges.map((e) => e.id), nodes.map((n) => [n.id, n.parentId, n.position, n.width, n.height, looks(n)])]);
    if (!w || key === shown.current) return;
    shown.current = key;
    dispose(w.root);
    w.root.clear();
    const box = build(w.root, nodes, edges, dark);
    if (!box.isEmpty()) w.light(box);
    if (!fitted.current && !box.isEmpty()) {
      w.fit(box);
      fitted.current = true;
    }
  }, [nodes, edges, dark]);

  useEffect(() => world.current?.select(selected), [selected, nodes, edges, dark]);

  return <div ref={host} className="absolute inset-0" />;
}

/** What of a node the scene draws, uptimes and the like left out. */
function looks(n: MapNode) {
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

/** A stable number in [0, 1) for a string: timings that survive rebuilds. */
function seed(s: string) {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 16777619);
  return (h >>> 0) / 2 ** 32;
}

/** Adds the scene's objects to root; returns their bounds. */
function build(root: THREE.Group, nodes: MapNode[], edges: Edge[], dark: boolean): THREE.Box3 {
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

/** A model, its feet at y = 0, and the height lines attach at. */
type Model = { obj: THREE.Object3D; top: number };

const FLOOR = 8;
const BLADE = 15;

function mat(color: number, o: { metal?: number; rough?: number; emissive?: number; glow?: number; opacity?: number; map?: THREE.Texture } = {}) {
  return new THREE.MeshStandardMaterial({
    color,
    metalness: o.metal ?? 0.1,
    roughness: o.rough ?? 0.6,
    emissive: o.emissive ?? 0x000000,
    emissiveIntensity: o.glow ?? 1,
    transparent: (o.opacity ?? 1) < 1,
    opacity: o.opacity ?? 1,
    map: o.map ?? null,
  });
}

function mesh(geo: THREE.BufferGeometry, m: THREE.Material, x = 0, y = 0, z = 0) {
  const o = new THREE.Mesh(geo, m);
  o.position.set(x, y, z);
  return o;
}

/** An LED in the state's color, blinking like disk activity while it runs. */
function led(geo: THREE.BufferGeometry, state: string, on: boolean, x: number, y: number, z: number, id: string) {
  const m = mat(on ? stateColor(state) : 0x3f3f46, { emissive: on ? stateColor(state) : 0, glow: 1.5 });
  const o = mesh(geo, m, x, y, z);
  if (on) {
    const rate = 2 + seed(id) * 6;
    const phase = seed(`${id}/phase`) * 10;
    o.userData.tick = (t: number) => (m.emissiveIntensity = Math.sin(t * rate + phase) > -0.3 ? 1.6 : 0.2);
  }
  return o;
}

/**
 * A project's floor: a darker slab, its tiles inset on top. The rim keeps
 * floors apart where their diamonds cross on screen.
 */
function floor(w: number, d: number, h: number, dark: boolean, ghost = false) {
  const g = new THREE.Group();
  const opacity = ghost ? 0.55 : 1;
  g.add(mesh(new RoundedBoxGeometry(w, h, d, 2, 3), mat(dark ? 0x3f3f46 : 0xb8b8c2, { rough: 0.7, opacity }), 0, h / 2, 0));
  const map = texture(dark ? "tiles-dark" : "tiles").clone();
  map.repeat.set(w / 64, d / 64);
  map.needsUpdate = true;
  g.add(mesh(new THREE.BoxGeometry(w - 8, 1, d - 8), mat(0xffffff, { map, rough: 0.85, opacity }), 0, h + 0.5, 0));
  return g;
}

/** An app: a server cabinet, one blade per replica, LEDs and a status bar in its state. */
function cabinet(w: number, d: number, blades: number, live: number, state: string, id: string): Model {
  const g = new THREE.Group();
  const cw = w * 0.6;
  const cd = d * 0.6;
  const h = 10 + blades * (BLADE + 3) + 6;
  g.add(mesh(new RoundedBoxGeometry(cw, h, cd, 2, 3), mat(0x2b303b, { metal: 0.6, rough: 0.4, map: texture("vents") }), 0, h / 2, 0));
  const bladeGeo = new THREE.BoxGeometry(cw * 0.88, BLADE, 2);
  const ledGeo = new THREE.BoxGeometry(4, 3, 1.5);
  for (let i = 0; i < blades; i++) {
    const on = i < live;
    const y = 10 + i * (BLADE + 3) + BLADE / 2;
    g.add(mesh(bladeGeo, mat(on ? 0x4b5563 : 0x374151, { metal: 0.5, rough: 0.35 }), 0, y, cd / 2 + 1));
    for (let k = 0; k < 3; k++) g.add(led(ledGeo, state, on, cw * 0.3 + k * 7, y, cd / 2 + 2.5, `${id}/${i}/${k}`));
    // Drive bays, as darker slots.
    for (let k = 0; k < 4; k++) g.add(mesh(new THREE.BoxGeometry(12, BLADE * 0.55, 1), mat(0x1f2937), -cw * 0.36 + k * 15, y, cd / 2 + 2.2));
  }
  // A status bar along the top, readable from afar.
  g.add(mesh(new THREE.BoxGeometry(cw * 0.9, 2.5, 3), mat(stateColor(state), { emissive: live ? stateColor(state) : 0, glow: 1.2 }), 0, h - 3, cd / 2 + 1));
  return { obj: g, top: h };
}

/** A database: the classic stack of disks, in its engine's color, a light in its state on top. */
function disks(r: number, kind: DatabaseKind, state: string, on: boolean): Model {
  const g = new THREE.Group();
  const body = mat(dbColor[kind], { metal: 0.25, rough: 0.35 });
  const rim = mat(0xffffff, { metal: 0.3, rough: 0.3, opacity: 0.55 });
  const disk = 20;
  const gap = 4;
  for (let i = 0; i < 3; i++) {
    const y = i * (disk + gap);
    g.add(mesh(new THREE.CylinderGeometry(r, r, disk, 48), body, 0, y + disk / 2, 0));
    // A light rim on each disk's top edge draws the icon's ellipses.
    const edge = mesh(new THREE.TorusGeometry(r - 1, 1.2, 8, 48), rim, 0, y + disk, 0);
    edge.rotation.x = Math.PI / 2;
    g.add(edge);
  }
  const h = 3 * disk + 2 * gap;
  const lightMat = mat(stateColor(state), { emissive: on ? stateColor(state) : 0, glow: 1.2 });
  const light = mesh(new THREE.CylinderGeometry(r * 0.22, r * 0.22, 3, 24), lightMat, 0, h + 1.5, 0);
  if (on) light.userData.tick = (t: number) => (lightMat.emissiveIntensity = 0.8 + 0.6 * Math.sin(t * 2));
  g.add(light);
  return { obj: g, top: h + 3 };
}

/** Traefik: a blue tower with port lights, an antenna on top. */
function gateway(w: number, d: number, id: string): Model {
  const g = new THREE.Group();
  const tw = w * 0.32;
  const td = d * 0.55;
  const h = 84;
  g.add(mesh(new RoundedBoxGeometry(tw, h, td, 2, 4), mat(0x0ea5e9, { metal: 0.35, rough: 0.35 }), 0, h / 2, 0));
  const ledGeo = new THREE.BoxGeometry(5, 4, 1.5);
  for (let r = 0; r < Math.floor(h / 22); r++) for (let c = 0; c < 4; c++) g.add(led(ledGeo, "running", true, -tw * 0.3 + c * (tw * 0.2), 14 + r * 22, td / 2 + 1, `${id}/${r}/${c}`));
  g.add(mesh(new THREE.CylinderGeometry(1.2, 1.2, 34, 8), mat(0x9ca3af, { metal: 0.8 }), tw * 0.25, h + 17, 0));
  const tipMat = mat(0xef4444, { emissive: 0xef4444 });
  const tip = mesh(new THREE.SphereGeometry(3, 12, 12), tipMat, tw * 0.25, h + 35, 0);
  tip.userData.tick = (t: number) => (tipMat.emissiveIntensity = Math.sin(t * 3) > 0 ? 2 : 0.1);
  g.add(tip);
  return { obj: g, top: h + 38 };
}

/**
 * The manager: kipitiny's mark as seen from above, a rounded square tray in
 * the foreground color, the yellow block standing in its front left corner.
 */
function manager(w: number, d: number, dark: boolean): Model {
  const g = new THREE.Group();
  const s = Math.min(w * 0.42, d * 0.8);
  const rim = s * 0.09;
  const h = 14;
  const rounded = (size: number, r: number) => {
    const p = new THREE.Shape();
    const a = size / 2;
    p.moveTo(-a + r, -a);
    p.lineTo(a - r, -a);
    p.quadraticCurveTo(a, -a, a, -a + r);
    p.lineTo(a, a - r);
    p.quadraticCurveTo(a, a, a - r, a);
    p.lineTo(-a + r, a);
    p.quadraticCurveTo(-a, a, -a, a - r);
    p.lineTo(-a, -a + r);
    p.quadraticCurveTo(-a, -a, -a + r, -a);
    return p;
  };
  const outline = rounded(s, s * 0.2);
  outline.holes.push(rounded(s - 2 * rim, s * 0.12));
  // Extruded up from the ground: the shape is drawn in x/y, turned to lie flat.
  const tray = mesh(new THREE.ExtrudeGeometry(outline, { depth: h, bevelEnabled: true, bevelThickness: 1.5, bevelSize: 1.5, bevelSegments: 2 }), mat(dark ? 0xe4e4e7 : 0x27272a, { metal: 0.3, rough: 0.4 }));
  tray.rotation.x = -Math.PI / 2;
  g.add(tray);
  g.add(mesh(new THREE.BoxGeometry(s - 2 * rim, 2, s - 2 * rim), mat(dark ? 0x27272a : 0xf4f4f5, { rough: 0.8 }), 0, 1, 0));
  const k = s * 0.24;
  const blockMat = mat(0xf5c518, { emissive: 0xf5c518, glow: 0.25, rough: 0.35 });
  const block = mesh(new RoundedBoxGeometry(k, k, k, 2, k * 0.15), blockMat, -s / 2 + rim + k * 0.85, k / 2 + 2, s / 2 - rim - k * 0.85);
  block.userData.tick = (t: number) => (blockMat.emissiveIntensity = 0.2 + 0.15 * Math.sin(t * 1.5));
  g.add(block);
  return { obj: g, top: Math.max(h, k + 2) };
}

/** The Cloudflare tunnel: an orange cloud, floating, drifting a little. */
function cloud(w: number, d: number): Model {
  const g = new THREE.Group();
  const r = Math.min(w, d) * 0.26;
  const y = r * 1.6 + 40;
  const puffs = new THREE.Group();
  const orange = mat(0xf38020, { rough: 0.55 });
  const light = mat(0xfbad41, { rough: 0.55 });
  // Cloudflare's cloud: a long flat-bottomed base, a small light bump on
  // the left, a big one on the right.
  puffs.add(mesh(new THREE.CapsuleGeometry(r * 0.55, r * 2.6, 8, 24).rotateZ(Math.PI / 2), orange, 0, 0, 0));
  puffs.add(mesh(new THREE.SphereGeometry(r * 0.75, 28, 20), light, -r * 0.75, r * 0.3, -r * 0.1));
  puffs.add(mesh(new THREE.SphereGeometry(r * 1.05, 28, 20), orange, r * 0.45, r * 0.55, -r * 0.15));
  // Squashed a little: clouds are wider than tall.
  puffs.scale.y = 0.85;
  puffs.position.y = y;
  puffs.userData.tick = (t: number) => (puffs.position.y = y + Math.sin(t * 0.9) * 4);
  g.add(puffs);
  const shade = mesh(new THREE.CircleGeometry(r * 2, 32), new THREE.MeshBasicMaterial({ color: 0x000000, transparent: true, opacity: 0.1 }), 0, 0.5, 0);
  shade.rotation.x = -Math.PI / 2;
  shade.scale.set(1.6, 0.8, 1);
  g.add(shade);
  return { obj: g, top: y + r * 1.25 };
}

/** The Internet: a globe floating over its spot, a satellite on its orbit. */
function globe(r: number): Model {
  const g = new THREE.Group();
  const y = r + 18;
  const world = new THREE.Group();
  world.add(mesh(new THREE.SphereGeometry(r, 40, 24), mat(0x0c4a6e, { rough: 0.45, metal: 0.2, map: texture("earth") })));
  world.add(mesh(new THREE.SphereGeometry(r * 1.01, 24, 12), new THREE.MeshBasicMaterial({ color: 0x7dd3fc, wireframe: true, transparent: true, opacity: 0.25 })));
  world.position.y = y;
  world.userData.tick = (t: number) => {
    world.rotation.y = t * 0.25;
    world.position.y = y + Math.sin(t * 1.2) * 3;
  };
  g.add(world);
  const orbit = new THREE.Group();
  orbit.position.y = y;
  orbit.rotation.set(0.5, 0, 0.3);
  const ring = mesh(new THREE.TorusGeometry(r * 1.45, 0.6, 6, 64), new THREE.MeshBasicMaterial({ color: 0x7dd3fc, transparent: true, opacity: 0.5 }));
  ring.rotation.x = Math.PI / 2;
  const sat = mesh(new THREE.BoxGeometry(5, 3, 3), mat(0xe5e7eb, { metal: 0.8, emissive: 0x38bdf8, glow: 0.4 }));
  sat.userData.tick = (t: number) => sat.position.set(Math.cos(t) * r * 1.45, 0, Math.sin(t) * r * 1.45);
  orbit.add(ring, sat);
  g.add(orbit);
  // Its shadow on the ground: a dark disc.
  const shade = mesh(new THREE.CircleGeometry(r * 0.8, 32), new THREE.MeshBasicMaterial({ color: 0x000000, transparent: true, opacity: 0.12 }), 0, 0.5, 0);
  shade.rotation.x = -Math.PI / 2;
  g.add(shade);
  return { obj: g, top: y + r };
}

/** A network created by hand: a violet hub, its ring pulsing. */
function hub(r: number): Model {
  const g = new THREE.Group();
  g.add(mesh(new THREE.CylinderGeometry(r * 0.5, r * 0.6, 10, 32), mat(0x6d28d9, { metal: 0.4, rough: 0.35 }), 0, 5, 0));
  const ringMat = mat(0xa78bfa, { emissive: 0x8b5cf6 });
  const ring = mesh(new THREE.TorusGeometry(r * 0.8, 2, 8, 48), ringMat, 0, 4, 0);
  ring.rotation.x = Math.PI / 2;
  ring.userData.tick = (t: number) => (ringMat.emissiveIntensity = 0.6 + 0.6 * Math.sin(t * 2.5));
  g.add(ring);
  return { obj: g, top: 12 };
}

/** Textures drawn on a canvas once, shared by every model. */
const textures = new Map<string, THREE.CanvasTexture>();
function texture(name: "tiles" | "tiles-dark" | "vents" | "earth"): THREE.CanvasTexture {
  const cached = textures.get(name);
  if (cached) return cached;
  const c = document.createElement("canvas");
  c.width = c.height = 128;
  const x = c.getContext("2d")!;
  if (name === "tiles" || name === "tiles-dark") {
    const dark = name === "tiles-dark";
    // Four tiles, each a slightly different shade, with grout between.
    for (let i = 0; i < 4; i++) {
      const v = (dark ? 42 : 226) + ((i * 7) % 5) - 2;
      x.fillStyle = `rgb(${v},${v},${v + (dark ? 3 : 2)})`;
      x.fillRect((i % 2) * 64, Math.floor(i / 2) * 64, 64, 64);
    }
    x.strokeStyle = dark ? "rgba(255,255,255,0.06)" : "rgba(0,0,0,0.08)";
    x.lineWidth = 2;
    x.strokeRect(1, 1, 126, 126);
    x.beginPath();
    x.moveTo(64, 0);
    x.lineTo(64, 128);
    x.moveTo(0, 64);
    x.lineTo(128, 64);
    x.stroke();
  } else if (name === "vents") {
    x.fillStyle = "#ffffff";
    x.fillRect(0, 0, 128, 128);
    x.fillStyle = "#b9bec8";
    for (let y = 6; y < 128; y += 8) x.fillRect(10, y, 108, 3);
  } else {
    // Continents as blotches on the ocean.
    x.fillStyle = "#1e6aa8";
    x.fillRect(0, 0, 128, 128);
    x.fillStyle = "#3fa36b";
    for (const [cx, cy, rx, ry] of [[28, 40, 18, 14], [40, 78, 10, 20], [76, 36, 24, 12], [82, 70, 12, 14], [110, 90, 10, 8]]) {
      x.beginPath();
      x.ellipse(cx, cy, rx, ry, 0.4, 0, Math.PI * 2);
      x.fill();
    }
  }
  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  t.wrapS = t.wrapT = THREE.RepeatWrapping;
  t.anisotropy = 4;
  textures.set(name, t);
  return t;
}

function dispose(root: THREE.Object3D) {
  root.traverse((o) => {
    if (o instanceof THREE.Mesh || o instanceof THREE.LineSegments) {
      o.geometry.dispose();
      (Array.isArray(o.material) ? o.material : [o.material]).forEach((m) => {
        // Floors clone a shared texture; the shared ones stay.
        if (m.map && ![...textures.values()].includes(m.map)) m.map.dispose();
        m.dispose();
      });
    }
    if (o instanceof CSS2DObject) o.element.remove();
  });
}
