import * as THREE from "three";
import { RoundedBoxGeometry } from "three/examples/jsm/geometries/RoundedBoxGeometry.js";
import { CSS2DObject } from "three/examples/jsm/renderers/CSS2DRenderer.js";
import type { DatabaseKind } from "@/api";

/* The 3D map's models, built from primitives and canvas textures. */

const stateColor = (state: string) =>
  ({ running: 0x10b981, healthy: 0x10b981, starting: 0xf59e0b, degraded: 0xf59e0b, restarting: 0xf59e0b, asleep: 0x3b82f6 })[state] ??
  (["failed", "unhealthy", "dead", "exited"].includes(state) ? 0xef4444 : 0x9ca3af);
const dbColor: Record<DatabaseKind, number> = { postgres: 0x336791, redis: 0xd82c20, mysql: 0x00758f, mariadb: 0x8a5a44, mongodb: 0x13aa52 };

/** A stable number in [0, 1) for a string: timings that survive rebuilds. */
export function seed(s: string) {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 16777619);
  return (h >>> 0) / 2 ** 32;
}

/** A model, its feet at y = 0, and the height lines attach at. */
export type Model = { obj: THREE.Object3D; top: number };

export const FLOOR = 8;
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
export function floor(w: number, d: number, h: number, dark: boolean, ghost = false) {
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
export function cabinet(w: number, d: number, blades: number, live: number, state: string, id: string): Model {
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
export function disks(r: number, kind: DatabaseKind, state: string, on: boolean): Model {
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
export function gateway(w: number, d: number, id: string): Model {
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
export function manager(w: number, d: number, dark: boolean): Model {
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
export function cloud(w: number, d: number): Model {
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
export function globe(r: number): Model {
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
export function hub(r: number): Model {
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

export function dispose(root: THREE.Object3D) {
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
