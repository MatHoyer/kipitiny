import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, RefreshCw, Trash2, Undo2 } from "lucide-react";
import { useRef, useState } from "react";
import { toast } from "sonner";
import { ErrorText } from "@/components/common";
import { CopyButton } from "@kipit-labs/kipitidy/copy-button";
import { Loading } from "@kipit-labs/kipitidy/loading";
import { Button } from "@kipit-labs/kipitidy/button";
import { formatBytes, formatDuration } from "@/lib/format";
import { cn } from "@/lib/utils";
import { api, type KeyChanges, type RedisKey, type RedisValue } from "../../api";
import { BrowserShell } from "./browser-shell";
import { ChangeBar, ReviewDialog } from "./changes";
import { CELL_MAX, DataTable, type Cell, type DataColumn, type GridEdit } from "./data-table";
import { usePending } from "./pending";
import { typeNames } from "./redis-types";
import { prettyValue } from "./values";

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

/** A key's value: a string whole, a collection's items in a grid, edited once writes are allowed. */
export function ValueView({
  serviceId,
  name,
  redisKey,
  sidebar,
  sidebarOpen,
  toolbarStart,
  toolbarEnd,
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
  const { pending, setPending, counts } = usePending(nothing, writeAllowed, (p) => (p.deleteKey ? 1 : 0));
  const [reviewing, setReviewing] = useState(false);
  const seq = useRef(0);
  const type = first?.type ?? "";
  const write = writeAllowed && !!first;
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
                    : counts
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
        <div className="flex h-full flex-col">
          {pending.deleteKey && <p className="shrink-0 border-b bg-destructive/5 px-3 py-2 text-xs text-destructive">Saving deletes this key.</p>}
          <div className={cn("min-h-0 flex-1 overflow-auto", pending.deleteKey && "text-muted-foreground line-through")}>
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
                    className="h-full max-h-none rounded-none border-0"
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
        </div>
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
