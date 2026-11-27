import type { Edge } from "@xyflow/react";
import { useEffect, useRef } from "react";
import * as THREE from "three";
import { MapControls } from "three/examples/jsm/controls/MapControls.js";
import { RoomEnvironment } from "three/examples/jsm/environments/RoomEnvironment.js";
import { CSS2DRenderer } from "three/examples/jsm/renderers/CSS2DRenderer.js";
import type { MapNode } from "./build";
import { dispose } from "./models3d";
import { build, looks } from "./scene3d";

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
