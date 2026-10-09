import { useInfiniteQuery } from "@tanstack/react-query";
import { ChevronRight, Clock, RefreshCw } from "lucide-react";
import { useMemo, useState } from "react";
import { CopyButton, ErrorText, Loading } from "@/components/common";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { formatBytes, formatDuration } from "@/lib/format";
import { cn } from "@/lib/utils";
import { api, type RedisKey, type RedisValue } from "../../api";
import { BrowserShell, SearchBox, SidebarToggle } from "./browser-shell";
import { CELL_MAX, DataTable, type DataColumn } from "./data-table";
import { prettyValue } from "./values";

/** Keys to gather per scan: a SCAN step may return few or none. */
const WANT = 200;

const typeNames: Record<string, string> = { zset: "sorted set" };

/** A bare word matches anywhere in the key; a glob (*, ?, [) is used as is. */
const toPattern = (s: string) => (!s.trim() ? "*" : /[*?[]/.test(s) ? s.trim() : `*${s.trim()}*`);

export function RedisBrowser({ serviceId }: { serviceId: string }) {
  const [match, setMatch] = useState("");
  const [picked, setPicked] = useState<string | null>(null);
  const [sidebar, setSidebar] = useState(true);
  const pattern = toPattern(match);
  const keys = useInfiniteQuery({
    queryKey: ["data", serviceId, "keys", pattern],
    initialPageParam: "0",
    // Gather SCAN steps until WANT keys or the end, so a sparse pattern still fills a page.
    queryFn: async ({ pageParam }) => {
      const out: RedisKey[] = [];
      let cursor = pageParam;
      do {
        const page = await api.redisScan(serviceId, cursor, pattern);
        out.push(...page.keys);
        cursor = page.cursor;
      } while (cursor !== "0" && out.length < WANT);
      return { keys: out, cursor };
    },
    getNextPageParam: (last) => (last.cursor === "0" ? undefined : last.cursor),
  });
  const list = useMemo(() => (keys.data?.pages.flatMap((p) => p.keys) ?? []).sort((a, b) => a.key.localeCompare(b.key)), [keys.data]);
  const tree = useMemo(() => buildTree(list), [list]);
  const current = picked ?? list[0]?.key ?? null;

  const nav = (
    <>
      <div className="flex h-12 shrink-0 items-center gap-1 border-b px-2">
        <span className="flex-1 px-1 text-sm font-medium">Keys</span>
        <Button variant="ghost" size="icon-xs" title="Rescan" onClick={() => keys.refetch()}>
          <RefreshCw className={cn(keys.isFetching && "animate-spin")} />
        </Button>
      </div>
      <div className="border-b p-2">
        <SearchBox value={match} onChange={setMatch} placeholder="Match keys" />
      </div>
      <nav aria-label="Keys" className="flex-1 overflow-y-auto p-2">
        {keys.isPending ? (
          <Loading className="py-6" />
        ) : list.length === 0 ? (
          <p className="px-2 text-xs text-muted-foreground">{pattern === "*" ? "No keys yet." : `No key matches ${pattern}.`}</p>
        ) : (
          <ul>
            {tree.map((n) => (
              <TreeNode key={n.path} node={n} depth={0} picked={current} onPick={setPicked} />
            ))}
          </ul>
        )}
        <ErrorText error={keys.error} />
      </nav>
      <div className="flex items-center gap-2 border-t px-3 py-2 text-xs text-muted-foreground">
        <span className="flex-1">
          {list.length} keys{keys.hasNextPage && " so far"}
        </span>
        {keys.hasNextPage && (
          <Button variant="outline" size="xs" loading={keys.isFetchingNextPage} onClick={() => keys.fetchNextPage()}>
            Scan further
          </Button>
        )}
      </div>
    </>
  );

  const picker = (
    <Select value={current ?? ""} onValueChange={setPicked}>
      <SelectTrigger size="sm" aria-label="Key" className="max-w-48 font-mono text-xs md:hidden">
        <SelectValue placeholder="Pick a key" />
      </SelectTrigger>
      <SelectContent>
        {list.map((k) => (
          <SelectItem key={k.key} value={k.key} className="font-mono text-xs">
            {k.key}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );

  if (!current) {
    return (
      <BrowserShell sidebar={nav} sidebarOpen={sidebar} toolbar={<SidebarToggle open={sidebar} onToggle={() => setSidebar(!sidebar)} />}>
        <p className="p-6 text-center text-sm text-muted-foreground">{keys.isPending ? "" : "Nothing to show."}</p>
      </BrowserShell>
    );
  }
  return (
    <ValueView
      key={current}
      serviceId={serviceId}
      name={current}
      redisKey={list.find((k) => k.key === current)}
      sidebar={nav}
      sidebarOpen={sidebar}
      toolbarStart={
        <>
          <SidebarToggle open={sidebar} onToggle={() => setSidebar(!sidebar)} />
          {picker}
        </>
      }
    />
  );
}

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

const fixedColumns: Record<string, string[]> = {
  hash: ["field", "value"],
  list: ["index", "value"],
  set: ["member"],
  zset: ["member", "score"],
};

/** Columns and rows for a collection; a stream gets one column per field name it uses. */
function grid(type: string, items: string[][], truncated: [number, number][]) {
  if (type !== "stream") return { columns: (fixedColumns[type] ?? ["value"]).map((name): DataColumn => ({ name })), rows: items as (string | null)[][], truncated };
  const fields: string[] = [];
  for (const [, ...fv] of items) for (let i = 0; i < fv.length; i += 2) if (!fields.includes(fv[i])) fields.push(fv[i]);
  const rows = items.map(([id, ...fv]) => {
    const row: (string | null)[] = [id, ...fields.map(() => null)];
    for (let i = 0; i < fv.length; i += 2) row[1 + fields.indexOf(fv[i])] = fv[i + 1];
    return row;
  });
  // Truncation positions refer to the flat entry; map them onto the columns.
  const cut = truncated.flatMap(([r, c]): [number, number][] => (c === 0 ? [[r, 0]] : c % 2 === 0 ? [[r, 1 + fields.indexOf(items[r][c - 1])]] : []));
  return { columns: [{ name: "id" }, ...fields.map((name) => ({ name }))], rows, truncated: cut };
}

function ValueView({
  serviceId,
  name,
  redisKey,
  sidebar,
  sidebarOpen,
  toolbarStart,
}: {
  serviceId: string;
  name: string;
  redisKey?: RedisKey;
  sidebar: React.ReactNode;
  sidebarOpen: boolean;
  toolbarStart: React.ReactNode;
}) {
  const pages = useInfiniteQuery({
    queryKey: ["data", serviceId, "key", name],
    initialPageParam: "0",
    queryFn: ({ pageParam }) => api.redisGet(serviceId, name, pageParam),
    getNextPageParam: (last) => last.cursor || undefined,
  });
  const first: RedisValue | undefined = pages.data?.pages[0];
  const items: string[][] = [];
  const truncated: [number, number][] = [];
  for (const p of pages.data?.pages ?? []) {
    truncated.push(...p.truncated.map(([r, c]): [number, number] => [r + items.length, c]));
    items.push(...p.items);
  }

  return (
    <BrowserShell
      sidebar={sidebar}
      sidebarOpen={sidebarOpen}
      toolbar={
        <>
          {toolbarStart}
          <span className="min-w-0 truncate px-1 font-mono text-xs font-medium" title={name}>
            {name}
          </span>
          <CopyButton value={name} label="Copy key" />
          {first && (
            <span className="flex flex-wrap gap-x-3 font-mono text-xs text-muted-foreground">
              <span>{typeNames[first.type] ?? first.type}</span>
              <span>{first.type === "string" ? formatBytes(first.length) : `${Intl.NumberFormat().format(first.length)} ${first.type === "hash" ? "fields" : "items"}`}</span>
              {redisKey && redisKey.bytes > 0 && <span>{formatBytes(redisKey.bytes)} in memory</span>}
              <span>{first.ttl < 0 ? "no expiry" : `expires in ${formatDuration(first.ttl)}`}</span>
            </span>
          )}
          <Button variant="outline" size="icon-sm" className="ml-auto" title="Reload value" onClick={() => pages.refetch()}>
            <RefreshCw className={cn(pages.isFetching && "animate-spin")} />
          </Button>
        </>
      }
      footer={
        first &&
        first.type !== "string" && (
          <>
            <span className="px-1 text-xs text-muted-foreground tabular-nums">
              {items.length} of {Intl.NumberFormat().format(first.length)}
            </span>
            {pages.hasNextPage && (
              <Button variant="outline" size="sm" loading={pages.isFetchingNextPage} onClick={() => pages.fetchNextPage()}>
                Load more
              </Button>
            )}
          </>
        )
      }
    >
      {pages.isPending ? (
        <Loading />
      ) : pages.error ? (
        <div className="p-3">
          <ErrorText error={pages.error} />
        </div>
      ) : first!.type === "string" ? (
        <div>
          <pre className="p-3 font-mono text-xs break-all whitespace-pre-wrap">{prettyValue(items[0]?.[0] ?? "", truncated.length > 0)}</pre>
          {truncated.length > 0 && <p className="border-t px-3 py-2 text-xs text-muted-foreground">First {formatBytes(CELL_MAX)} of {formatBytes(first!.length)} shown.</p>}
        </div>
      ) : (
        (() => {
          const g = grid(first!.type, items, truncated);
          return (
            <DataTable
              className="max-h-none rounded-none border-0"
              columns={g.columns}
              rows={g.rows}
              truncated={g.truncated}
              rowLabel={(r) => (first!.type === "list" ? `index ${r[0]}` : (r[0] ?? ""))}
              empty="Empty."
            />
          );
        })()
      )}
    </BrowserShell>
  );
}
