import { ChevronRight, Clock } from "lucide-react";
import { useMemo, useState } from "react";
import { formatDuration } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { RedisKey } from "../../api";

type Node = { label: string; path: string; key?: RedisKey; children: Node[]; count: number };

/** Groups keys by their ":" segments, the usual redis namespacing (user:42:sessions). */
function buildTree(keys: RedisKey[]): Node[] {
  const root: Node = { label: "", path: "", children: [], count: 0 };
  for (const k of keys) {
    const parts = k.key.split(":");
    let node = root;
    for (let i = 0; i < parts.length - 1; i++) {
      const path = parts.slice(0, i + 1).join(":") + ":";
      let child = node.children.find((c) => c.path === path && !c.key);
      if (!child) node.children.push((child = { label: parts[i] + ":", path, children: [], count: 0 }));
      child.count++;
      node = child;
    }
    node.children.push({ label: parts[parts.length - 1] || k.key, path: k.key, key: k, children: [], count: 1 });
  }
  // A namespace holding a single key is noise: show the key in full instead.
  const flatten = (nodes: Node[]): Node[] =>
    nodes.flatMap((n) => {
      if (n.key) return [n];
      if (n.count === 1) {
        const leaf = (function first(x: Node): Node {
          return x.key ? x : first(x.children[0]);
        })(n);
        return [{ ...leaf, label: leaf.key!.key.slice(n.path.length - n.label.length) }];
      }
      // A namespace holding only another one reads as one: cache:page:.
      let m = n;
      while (m.children.length === 1 && !m.children[0].key) m = { ...m.children[0], label: m.label + m.children[0].label };
      return [{ ...m, children: flatten(m.children) }];
    });
  return flatten(root.children);
}

function TreeNode({ node, depth, picked, onPick }: { node: Node; depth: number; picked: string | null; onPick: (k: string) => void }) {
  const [open, setOpen] = useState(depth === 0 && node.count <= 20);
  const pad = { paddingLeft: `${0.5 + depth * 0.9}rem` };
  if (node.key) {
    const k = node.key;
    return (
      <li>
        <button
          type="button"
          onClick={() => onPick(k.key)}
          aria-current={picked === k.key}
          title={k.key}
          style={pad}
          className={cn(
            "flex w-full items-center gap-2 rounded-md py-1 pr-2 text-left font-mono text-xs text-muted-foreground hover:bg-muted hover:text-foreground",
            picked === k.key && "bg-muted text-foreground",
          )}
        >
          <span className="min-w-0 flex-1 truncate">{node.label}</span>
          {k.ttl >= 0 && <Clock className="size-3 shrink-0" aria-label={`expires in ${formatDuration(k.ttl)}`} />}
          <span className="shrink-0 text-[0.65rem] opacity-70">{k.type}</span>
        </button>
      </li>
    );
  }
  return (
    <li>
      <button
        type="button"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        style={pad}
        className="flex w-full items-center gap-1 rounded-md py-1 pr-2 text-left font-mono text-xs text-muted-foreground hover:bg-muted hover:text-foreground"
      >
        <ChevronRight className={cn("-ml-1 size-3.5 shrink-0 transition-transform motion-reduce:transition-none", open && "rotate-90")} />
        <span className="min-w-0 flex-1 truncate">{node.label}</span>
        <span className="tabular-nums">{node.count}</span>
      </button>
      {open && (
        <ul>
          {node.children.map((c) => (
            <TreeNode key={c.path} node={c} depth={depth + 1} picked={picked} onPick={onPick} />
          ))}
        </ul>
      )}
    </li>
  );
}

/** The keys as a tree of their namespaces. */
export function KeyTree({ keys, picked, onPick }: { keys: RedisKey[]; picked: string | null; onPick: (k: string) => void }) {
  const tree = useMemo(() => buildTree(keys), [keys]);
  return (
    <ul>
      {tree.map((n) => (
        <TreeNode key={n.path} node={n} depth={0} picked={picked} onPick={onPick} />
      ))}
    </ul>
  );
}
