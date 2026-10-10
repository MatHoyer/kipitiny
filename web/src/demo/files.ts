import type { VolumeEntry, VolumeFile, VolumeListing } from "@/api";

/*
 * The demo's volume files: an in-memory tree per service, reset on reload.
 * Uploads keep their size and, for text, their content.
 */

type Node = { type: "dir"; children: Map<string, Node>; modified: string } | { type: "file"; content: string; size: number; modified: string };

const at = (daysAgo: number) => new Date(Date.UTC(2026, 8, 30, 9) - daysAgo * 86_400_000).toISOString();

function dir(entries: Record<string, Node>, modified = at(3)): Node {
  return { type: "dir", children: new Map(Object.entries(entries)), modified };
}
function file(content: string, modified = at(3), size = new TextEncoder().encode(content).length): Node {
  return { type: "file", content, size, modified };
}

/** Each service's volumes, by name, with where they're mounted. */
const trees = new Map<string, Map<string, { mountPath: string; root: Node }>>();

function volumesOf(service: { id: string; kind: string; volumes: { name: string; path: string }[] }) {
  let vols = trees.get(service.id);
  if (vols) return vols;
  vols = new Map();
  if (service.kind === "postgres")
    vols.set("data", {
      mountPath: "/var/lib/postgresql/data",
      root: dir({
        PG_VERSION: file("17\n"),
        base: dir({ "1": dir({}), "16384": dir({ "1259": file("", at(0), 106_496), "2608": file("", at(0), 155_648) }) }),
        global: dir({ pg_control: file("", at(0), 8_192) }),
        pg_wal: dir({ "000000010000000000000001": file("", at(0), 16_777_216) }),
        "postgresql.conf": file("listen_addresses = '*'\nshared_buffers = 128MB\n"),
        "postmaster.pid": file("1\n/var/lib/postgresql/data\n", at(0)),
      }),
    });
  else if (service.kind === "redis")
    vols.set("data", {
      mountPath: "/data",
      root: dir({ appendonlydir: dir({ "appendonly.aof.1.base.rdb": file("", at(0), 182_340) }), "dump.rdb": file("", at(1), 180_112) }),
    });
  else
    for (const v of service.volumes)
      vols.set(v.name, {
        mountPath: v.path,
        root: dir({
          avatars: dir({ "u_1.png": file("", at(5), 48_211), "u_2.png": file("", at(2), 51_870) }),
          "README.txt": file("User uploads live here. Back them up with the service.\n"),
        }),
      });
  trees.set(service.id, vols);
  return vols;
}

class FilesError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

/** Resolves "<volume>/<path>" to its parent folder and name. */
function locate(service: Parameters<typeof volumesOf>[0], p: string) {
  const segs = p.split("/").filter((s) => s && s !== ".");
  if (segs.includes("..")) throw new FilesError(400, `path ${p} must not contain ..`);
  const vol = volumesOf(service).get(segs[0] ?? "");
  if (!vol) throw new FilesError(404, "not found");
  let parent: Node | undefined;
  let node: Node | undefined = vol.root;
  for (const s of segs.slice(1)) {
    if (node?.type !== "dir") throw new FilesError(404, "not found");
    parent = node;
    node = node.children.get(s);
  }
  return { vol, segs, parent, node, name: segs[segs.length - 1] };
}

function entry(name: string, n: Node): VolumeEntry {
  return { name, type: n.type, size: n.type === "file" ? n.size : 0, modified: n.modified, mode: n.type === "dir" ? "0755" : "0644", uid: 1000, gid: 1000 };
}

function guardWrite(service: { kind: string }) {
  if (service.kind !== "app") throw new FilesError(400, "the volumes of databases are read-only");
}

/** Like core: parent folders made on the way, conflicts refused. */
function mkdirs(service: Parameters<typeof volumesOf>[0], segs: string[]): Node {
  const vol = volumesOf(service).get(segs[0]);
  if (!vol) throw new FilesError(404, "not found");
  let node = vol.root;
  for (const s of segs.slice(1)) {
    if (node.type !== "dir") throw new FilesError(400, "not a folder");
    let next = node.children.get(s);
    if (!next) node.children.set(s, (next = dir({}, new Date().toISOString())));
    node = next;
  }
  return node;
}

function clone(n: Node): Node {
  return n.type === "dir" ? { ...n, children: new Map([...n.children].map(([k, v]) => [k, clone(v)])) } : { ...n };
}

type Service = Parameters<typeof volumesOf>[0];

export const files = {
  list(service: Service, p: string): VolumeListing {
    const readOnly = service.kind !== "app";
    if (!p.replace(/\/+/g, ""))
      return {
        path: "",
        readOnly,
        truncated: false,
        entries: [...volumesOf(service)].map(([name, v]) => ({ name, type: "volume", size: 0, uid: 0, gid: 0, mountPath: v.mountPath })),
      };
    const { node, segs } = locate(service, p);
    if (!node) throw new FilesError(404, "not found");
    if (node.type !== "dir") throw new FilesError(400, `${p} is not the expected kind of entry (file or folder)`);
    const entries = [...node.children].map(([name, n]) => entry(name, n));
    entries.sort((a, b) => (a.type === "dir") !== (b.type === "dir") ? (a.type === "dir" ? -1 : 1) : a.name.localeCompare(b.name));
    return { path: segs.join("/"), readOnly, truncated: false, entries };
  },
  read(service: Service, p: string): VolumeFile {
    const { node, segs, name } = locate(service, p);
    if (!node) throw new FilesError(404, "not found");
    if (node.type !== "file") throw new FilesError(400, `${p} is not the expected kind of entry (file or folder)`);
    if (node.size > 0 && node.content === "") throw new FilesError(400, `${p} is not a text file: download it instead`);
    return { ...entry(name, node), path: segs.join("/"), readOnly: service.kind !== "app", content: node.content };
  },
  write(service: Service, p: string, body: unknown, q: URLSearchParams): VolumeEntry {
    guardWrite(service);
    const { segs } = locate(service, segs0(p));
    const parent = mkdirs(service, segs.slice(0, -1));
    if (parent.type !== "dir") throw new FilesError(400, "not a folder");
    const name = segs[segs.length - 1];
    const old = parent.children.get(name);
    if (old && q.get("overwrite") !== "true") throw new FilesError(409, `${p} already exists`);
    if (old?.type === "dir") throw new FilesError(400, `${p} is not the expected kind of entry (file or folder)`);
    const modified = q.get("modified");
    if (modified && old && Math.floor(Date.parse(old.modified) / 1000) !== Math.floor(Date.parse(modified) / 1000))
      throw new FilesError(409, `${p} changed since it was read`);
    const content = typeof body === "string" ? body : "";
    const size = body instanceof Blob ? body.size : new TextEncoder().encode(content).length;
    const n = file(content, new Date().toISOString(), size);
    parent.children.set(name, n);
    return entry(name, n);
  },
  mkdir(service: Service, p: string) {
    guardWrite(service);
    const { segs, parent, node } = locate(service, segs0(p));
    if (node) throw new FilesError(409, `${p} already exists`);
    if (!parent) throw new FilesError(400, `give a path inside a volume, not ${p}`);
    mkdirs(service, segs);
  },
  transfer(service: Service, from: string, to: string, copy: boolean) {
    guardWrite(service);
    const src = locate(service, from);
    const dst = locate(service, to);
    if (!src.node || !src.parent || src.parent.type !== "dir") throw new FilesError(404, "not found");
    if (dst.node) throw new FilesError(409, `${to} already exists`);
    if (!dst.parent || dst.parent.type !== "dir") throw new FilesError(404, "not found");
    if (`${to}/`.startsWith(`${from}/`)) throw new FilesError(400, `can't move or copy ${from} into itself`);
    dst.parent.children.set(dst.name, copy ? clone(src.node) : src.node);
    if (!copy) src.parent.children.delete(src.name);
  },
  remove(service: Service, paths: string[]) {
    guardWrite(service);
    const found = paths.map((p) => locate(service, p));
    if (found.some((f) => !f.node || !f.parent)) throw new FilesError(404, "not found");
    for (const f of found) if (f.parent?.type === "dir") f.parent.children.delete(f.name);
  },
  FilesError,
};

const segs0 = (p: string) => p.replace(/^\/+/, "");
