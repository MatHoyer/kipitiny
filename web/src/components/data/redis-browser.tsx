import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, Clock, Plus, RefreshCw, Trash2, Undo2 } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { CopyButton, ErrorText, Loading } from "@/components/common";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Segmented, SegmentedItem } from "@/components/ui/segmented";
import { formatBytes, formatDuration } from "@/lib/format";
import { cn } from "@/lib/utils";
import { api, type KeyChanges, type RedisKey, type RedisValue } from "../../api";
import { BrowserShell, SearchBox, SidebarToggle } from "./browser-shell";
import { ChangeBar, ReviewDialog, useUnsaved } from "./changes";
import { CELL_MAX, DataTable, type Cell, type DataColumn, type GridEdit } from "./data-table";
import { prettyValue } from "./values";
import { WriteMode } from "./write-mode";

/** Keys to gather per scan: a SCAN step may return few or none. */
const WANT = 200;

const typeNames: Record<string, string> = { zset: "sorted set" };

/** A bare word matches anywhere in the key; a glob (*, ?, [) is used as is. */
const toPattern = (s: string) => (!s.trim() ? "*" : /[*?[]/.test(s) ? s.trim() : `*${s.trim()}*`);

export function RedisBrowser({ serviceId }: { serviceId: string }) {
  const [match, setMatch] = useState("");
  const [picked, setPicked] = useState<string | null>(null);
  const [sidebar, setSidebar] = useState(true);
  // Kept across keys: switching doesn't lock it again.
  const [write, setWrite] = useState(false);
  const { confirm } = useUnsaved();
  const pick = (key: string) => confirm(() => setPicked(key));
  const [creating, setCreating] = useState(false);
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
              <TreeNode key={n.path} node={n} depth={0} picked={current} onPick={pick} />
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
    <Select value={current ?? ""} onValueChange={pick}>
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

  const writeSwitch = (
    <WriteMode
      write={write}
      onChange={(w) => (w ? setWrite(true) : confirm(() => setWrite(false)))}
      description="You can then add keys, and edit, add and delete items or whole keys. Nothing is written until you review the commands and save; only a restore from a backup undoes a save."
    />
  );
  const newKey = write && (
    <Button variant="outline" size="sm" onClick={() => setCreating(true)}>
      <Plus /> Key
    </Button>
  );
  const dialog = (
    <NewKeyDialog
      open={creating}
      onOpenChange={setCreating}
      serviceId={serviceId}
      onCreated={(key) => {
        setPicked(key);
        keys.refetch();
      }}
    />
  );

  if (!current) {
    return (
      <BrowserShell
        sidebar={nav}
        sidebarOpen={sidebar}
        write={write}
        toolbar={
          <>
            <SidebarToggle open={sidebar} onToggle={() => setSidebar(!sidebar)} />
            <span className="ml-auto flex items-center gap-1.5">
              {newKey}
              {writeSwitch}
            </span>
            {dialog}
          </>
        }
      >
        <p className="p-6 text-center text-sm text-muted-foreground">{keys.isPending ? "" : write ? "No keys yet: + Key adds one." : "Nothing to show."}</p>
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
      write={write}
      toolbarStart={
        <>
          <SidebarToggle open={sidebar} onToggle={() => setSidebar(!sidebar)} />
          {picker}
        </>
      }
      toolbarEnd={
        <>
          {writeSwitch}
          {dialog}
        </>
      }
      newKey={newKey}
    />
  );
}

const newKeyColumns: Record<string, string[]> = { hash: ["field", "value"], list: ["value"], set: ["member"], zset: ["member", "score"] };

/** Creates a key with its first items, after the same review as any other save. */
function NewKeyDialog({ open, onOpenChange, serviceId, onCreated }: { open: boolean; onOpenChange: (o: boolean) => void; serviceId: string; onCreated: (key: string) => void }) {
  const [name, setName] = useState("");
  const [type, setType] = useState("string");
  const [value, setValue] = useState("");
  const [rows, setRows] = useState<string[][]>([["", ""]]);
  const [reviewing, setReviewing] = useState(false);
  const cols = newKeyColumns[type] ?? [];
  const filled = rows.filter((r) => r.slice(0, cols.length).some((c) => c !== ""));
  const changes = (): KeyChanges => ({
    type,
    create: true,
    inserts:
      type === "string"
        ? [[value]]
        : filled.map((r) => (type === "list" ? ["end", r[0]] : type === "zset" ? [r[0], r[1] || "0"] : r.slice(0, cols.length))),
  });
  const close = (o: boolean) => {
    onOpenChange(o);
    if (!o) {
      setName("");
      setType("string");
      setValue("");
      setRows([["", ""]]);
    }
  };
  const ready = name.trim() !== "" && (type === "string" || filled.length > 0);

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className="flex max-h-[85vh] flex-col sm:max-w-lg">
        <form
          className="contents"
          onSubmit={(e) => {
            e.preventDefault();
            if (ready) setReviewing(true);
          }}
        >
          <DialogHeader>
            <DialogTitle>New key</DialogTitle>
            <DialogDescription>A key with its first value or items, written when you save. It never expires.</DialogDescription>
          </DialogHeader>
          <div className="min-h-0 space-y-4 overflow-y-auto">
            <FloatingInput label="Key" autoFocus autoComplete="off" spellCheck={false} value={name} onChange={(e) => setName(e.target.value)} inputClassName="font-mono" />
            <Segmented size="sm" value={type} onValueChange={setType} aria-label="Type">
              {["string", "hash", "list", "set", "zset"].map((t) => (
                <SegmentedItem key={t} value={t}>
                  {typeNames[t] ?? t}
                </SegmentedItem>
              ))}
            </Segmented>
            {type === "string" ? (
              <textarea
                aria-label="Value"
                spellCheck={false}
                value={value}
                onChange={(e) => setValue(e.target.value)}
                placeholder="Value"
                className="field-sizing-content min-h-24 w-full resize-none rounded-md border bg-background p-3 font-mono text-xs outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
              />
            ) : (
              <div className="space-y-1.5">
                {rows.map((r, i) => (
                  <div key={i} className="flex items-center gap-1.5">
                    {cols.map((c, j) => (
                      <input
                        key={c}
                        aria-label={`${c} ${i + 1}`}
                        placeholder={c === "score" ? "0" : c}
                        spellCheck={false}
                        value={r[j] ?? ""}
                        inputMode={c === "score" ? "decimal" : undefined}
                        onChange={(e) => setRows(rows.map((x, k) => (k === i ? x.map((y, m) => (m === j ? e.target.value : y)) : x)))}
                        className={cn(
                          "h-8 min-w-0 rounded-md border bg-background px-2 font-mono text-xs outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50",
                          c === "score" ? "w-24" : "flex-1",
                        )}
                      />
                    ))}
                    <Button type="button" variant="ghost" size="icon-xs" title="Remove this item" disabled={rows.length === 1} onClick={() => setRows(rows.filter((_, k) => k !== i))}>
                      <Trash2 />
                    </Button>
                  </div>
                ))}
                <Button type="button" variant="ghost" size="xs" onClick={() => setRows([...rows, ["", ""]])}>
                  <Plus /> Item
                </Button>
              </div>
            )}
          </div>
          <DialogFooter>
            <Button type="submit" disabled={!ready}>
              Review
            </Button>
          </DialogFooter>
        </form>
        <ReviewDialog
          open={reviewing}
          onOpenChange={setReviewing}
          atomic="They run at once, only if the key doesn't exist yet."
          run={(apply) => api.keyChanges(serviceId, name.trim(), changes(), apply)}
          onSaved={() => {
            toast.success(`${name.trim()} created`);
            onCreated(name.trim());
            close(false);
          }}
        />
      </DialogContent>
    </Dialog>
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

/** Edits waiting for Save, items found by their first part (field, index, member, entry id); a string's value under "". */
type Pending = {
  edits: Record<string, { item: string[]; set: string[] }>;
  deletes: Record<string, string[]>;
  /** Newest first, as shown. */
  inserts: { id: number; item: string[] }[];
  deleteKey: boolean;
};

const nothing: Pending = { edits: {}, deletes: {}, inserts: [], deleteKey: false };

/** A new item of each type: a list's goes at the end, whatever its index says. */
const newItem: Record<string, string[]> = { hash: ["", ""], list: ["end", ""], set: [""], zset: ["", "0"] };

function ValueView({
  serviceId,
  name,
  redisKey,
  sidebar,
  sidebarOpen,
  toolbarStart,
  toolbarEnd,
  newKey,
  write: writeAllowed,
}: {
  serviceId: string;
  name: string;
  redisKey?: RedisKey;
  sidebar: React.ReactNode;
  sidebarOpen: boolean;
  toolbarStart: React.ReactNode;
  /** The write switch, just before Reload. */
  toolbarEnd: React.ReactNode;
  newKey: React.ReactNode;
  write: boolean;
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
  const queryClient = useQueryClient();
  const { setDirty } = useUnsaved();
  const [pending, setPending] = useState<Pending>(nothing);
  const [reviewing, setReviewing] = useState(false);
  const seq = useRef(0);
  // Turning writes off (once agreed to) drops what was pending.
  useEffect(() => {
    if (!writeAllowed) setPending(nothing);
  }, [writeAllowed]);
  const type = first?.type ?? "";
  const write = writeAllowed && !!first;
  const edited = Object.keys(pending.edits).filter((k) => !pending.deletes[k]).length;
  const deleted = Object.keys(pending.deletes).length;
  const dirty = pending.deleteKey ? 1 : edited + deleted + pending.inserts.length;
  useEffect(() => setDirty(dirty), [dirty, setDirty]);
  useEffect(() => () => setDirty(0), [setDirty]);
  const changes = (): KeyChanges =>
    pending.deleteKey
      ? { type, delete: true }
      : {
          type,
          edits: Object.entries(pending.edits)
            .filter(([k]) => !pending.deletes[k])
            .map(([, e]) => e),
          deletes: Object.values(pending.deletes),
          inserts: pending.inserts.map((ins) => ins.item).reverse(),
        };
  const cut = new Set(truncated.map(([r, c]) => `${r}:${c}`));
  const nNew = write ? pending.inserts.length : 0;
  const cutLocked = `Only the first ${formatBytes(CELL_MAX)} are loaded: change it from the console.`;
  const edit: GridEdit | undefined =
    write && !pending.deleteKey && type !== "string"
      ? {
          state: (i) => (i < nNew ? "new" : pending.deletes[items[i - nNew][0]] ? "deleted" : undefined),
          was: (i, j) => {
            if (i < nNew || type === "stream") return undefined;
            const it = items[i - nNew];
            const e = pending.edits[it[0]];
            return e && e.set[j] !== it[j] ? { value: it[j] } : undefined;
          },
          locked: (i, j) => {
            if (j < 0) return i >= nNew && cut.has(`${i - nNew}:0`) ? cutLocked : undefined;
            if (type === "stream") return "A stream's entries can only be deleted here: add them from the console.";
            if (type === "list" && j === 0) return "The index is the item's place: a new item goes at the end.";
            if (i < nNew) return undefined;
            if (type === "hash" && j === 0) return "A field can't be renamed: add the new one and delete this one.";
            if (cut.has(`${i - nNew}:${j}`) || cut.has(`${i - nNew}:0`)) return cutLocked;
            return undefined;
          },
          onEdit: (i, j, v) => {
            if (typeof v !== "string") return;
            if (i < nNew) {
              setPending((p) => ({ ...p, inserts: p.inserts.map((ins, k) => (k === i ? { ...ins, item: ins.item.map((x, c) => (c === j ? v : x)) } : ins)) }));
              return;
            }
            const it = items[i - nNew];
            setPending((p) => {
              const set = (p.edits[it[0]]?.set ?? it).map((x, c) => (c === j ? v : x));
              const edits = { ...p.edits };
              if (set.every((x, c) => x === it[c])) delete edits[it[0]];
              else edits[it[0]] = { item: it, set };
              return { ...p, edits };
            });
          },
          onDelete: (i) => {
            if (i < nNew) {
              setPending((p) => ({ ...p, inserts: p.inserts.filter((_, k) => k !== i) }));
              return;
            }
            const it = items[i - nNew];
            setPending((p) => {
              const deletes = { ...p.deletes };
              if (deletes[it[0]]) delete deletes[it[0]];
              else deletes[it[0]] = it;
              return { ...p, deletes };
            });
          },
        }
      : undefined;
  // What the grid shows: new items first, edits applied.
  const shownItems: string[][] = write
    ? [...pending.inserts.map((ins) => ins.item), ...items.map((it) => pending.edits[it[0]]?.set ?? it)]
    : items;
  const shownCut = truncated.map(([r, c]): [number, number] => [r + nNew, c]);

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
          <span className="ml-auto flex flex-wrap items-center gap-1.5">
            {newKey}
            {write && type !== "string" && type !== "stream" && !pending.deleteKey && (
              <Button variant="outline" size="sm" onClick={() => setPending((p) => ({ ...p, inserts: [{ id: ++seq.current, item: newItem[type] }, ...p.inserts] }))}>
                <Plus /> Item
              </Button>
            )}
            {write && (
              <Button
                variant="outline"
                size="sm"
                className={cn(!pending.deleteKey && "hover:text-destructive")}
                onClick={() => setPending((p) => (p.deleteKey ? nothing : { ...nothing, deleteKey: true }))}
              >
                {pending.deleteKey ? (
                  <>
                    <Undo2 /> Keep key
                  </>
                ) : (
                  <>
                    <Trash2 /> Delete key
                  </>
                )}
              </Button>
            )}
            {toolbarEnd}
            <Button variant="outline" size="icon-sm" title="Reload value" onClick={() => pages.refetch()}>
              <RefreshCw className={cn(pages.isFetching && "animate-spin")} />
            </Button>
          </span>
        </>
      }
      footer={
        first && (first.type !== "string" || write) && (
          <>
            {first.type !== "string" && (
              <span className="px-1 text-xs text-muted-foreground tabular-nums">
                {items.length} of {Intl.NumberFormat().format(first.length)}
              </span>
            )}
            {first.type !== "string" && pages.hasNextPage && (
              <Button variant="outline" size="sm" loading={pages.isFetchingNextPage} onClick={() => pages.fetchNextPage()}>
                Load more
              </Button>
            )}
            {write && (
              <ChangeBar
                counts={
                  pending.deleteKey
                    ? [{ n: 1, label: "key to delete", tone: "delete" }]
                    : [
                        { n: edited, label: "edited", tone: "edit" },
                        { n: pending.inserts.length, label: "new", tone: "new" },
                        { n: deleted, label: "to delete", tone: "delete" },
                      ]
                }
                onDiscard={() => setPending(nothing)}
                onSave={() => setReviewing(true)}
              />
            )}
            <ReviewDialog
              open={reviewing}
              onOpenChange={setReviewing}
              atomic="They run at once, after checking that what you changed is still as you loaded it: if not, nothing is written."
              run={(apply) => api.keyChanges(serviceId, name, changes(), apply)}
              onSaved={(res) => {
                const gone = pending.deleteKey;
                setPending(nothing);
                toast.success(gone ? `${name} deleted` : res.statements.length === 1 ? "1 change saved" : `${res.statements.length} changes saved`);
                queryClient.invalidateQueries({ queryKey: ["data", serviceId, "key", name] });
                queryClient.invalidateQueries({ queryKey: ["data", serviceId, "keys"] });
              }}
            />
          </>
        )
      }
      write={write}
    >
      {pages.isPending ? (
        <Loading />
      ) : pages.error ? (
        <div className="p-3">
          <ErrorText error={pages.error} />
        </div>
      ) : (
        <>
          {pending.deleteKey && <p className="border-b bg-destructive/5 px-3 py-2 text-xs text-destructive">Saving deletes this key.</p>}
          <div className={cn(pending.deleteKey && "text-muted-foreground line-through")}>
            {first!.type === "string" ? (
              write && !pending.deleteKey && truncated.length === 0 ? (
                <StringEditor
                  value={items[0]?.[0] ?? ""}
                  edited={pending.edits[""]?.set[0]}
                  onChange={(v) =>
                    setPending((p) => {
                      const edits = { ...p.edits };
                      if (v === (items[0]?.[0] ?? "")) delete edits[""];
                      else edits[""] = { item: [items[0]?.[0] ?? ""], set: [v] };
                      return { ...p, edits };
                    })
                  }
                />
              ) : (
                <div>
                  <pre className="p-3 font-mono text-xs break-all whitespace-pre-wrap">{prettyValue(items[0]?.[0] ?? "", truncated.length > 0)}</pre>
                  {truncated.length > 0 && (
                    <p className="border-t px-3 py-2 text-xs text-muted-foreground">
                      First {formatBytes(CELL_MAX)} of {formatBytes(first!.length)} shown{write && ": change it from the console"}.
                    </p>
                  )}
                </div>
              )
            ) : (
              (() => {
                const g = grid(first!.type, shownItems, shownCut);
                return (
                  <DataTable
                    className="max-h-none rounded-none border-0"
                    columns={g.columns}
                    rows={g.rows as Cell[][]}
                    truncated={g.truncated}
                    rowLabel={(r, i) => (i < nNew ? "New item" : first!.type === "list" ? `index ${r[0]}` : (r[0] ?? ""))}
                    empty="Empty."
                    edit={edit}
                  />
                );
              })()
            )}
          </div>
        </>
      )}
    </BrowserShell>
  );
}

/** A string's value, edited whole; the change is kept as you type. */
function StringEditor({ value, edited, onChange }: { value: string; edited?: string; onChange: (v: string) => void }) {
  return (
    <div className="p-3">
      <textarea
        aria-label="Value"
        spellCheck={false}
        value={edited ?? value}
        onChange={(e) => onChange(e.target.value)}
        className={cn(
          "field-sizing-content min-h-24 w-full resize-none rounded-md border bg-background p-3 font-mono text-xs leading-relaxed outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50",
          edited !== undefined && "border-amber-500 bg-amber-500/10",
        )}
      />
      {edited !== undefined && (
        <p className="mt-1.5 font-mono text-xs break-all text-muted-foreground">
          was {value.length > 200 ? `${value.slice(0, 200)}…` : value || '""'}
        </p>
      )}
    </div>
  );
}
