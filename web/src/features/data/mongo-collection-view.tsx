import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDownUp, ListFilter, Plus, RefreshCw } from "lucide-react";
import { useRef, useState, type ReactNode } from "react";
import { toast } from "sonner";
import { ErrorText } from "@/components/common";
import { Loading } from "@kipit-labs/kipitidy/loading";
import { Button } from "@kipit-labs/kipitidy/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@kipit-labs/kipitidy/select";
import { cn } from "@/lib/utils";
import { api, type DocChanges, type PgTable } from "../../api";
import { BrowserShell, SidebarToggle } from "./browser-shell";
import { ChangeBar, ReviewDialog, useUnsaved } from "./changes";
import { DocEditor, Document, JsonField } from "./mongo-document";
import { Pager } from "./pager";
import { usePending } from "./pending";
import { DropButton } from "./schema-dialogs";

const PAGE_SIZES = [10, 25, 50, 100];

type Doc = Record<string, unknown>;

/** An edited document: its new text, and what changed at the top level. */
type DocEdit = { id: string; text: string; set: Record<string, string>; unset: string[]; was: Record<string, string | null> };

/** Edits waiting for Save, documents found by their _id (as JSON). */
type Pending = {
  edits: Record<string, DocEdit>;
  deletes: Record<string, string>;
  /** Newest first, as shown. */
  inserts: { id: number; text: string }[];
};

const nothing: Pending = { edits: {}, deletes: {}, inserts: [] };

const parseDoc = (json: string): Doc | null => {
  try {
    const d = JSON.parse(json);
    return d && typeof d === "object" && !Array.isArray(d) ? d : null;
  } catch {
    return null;
  }
};

/** The top-level fields that differ, with their loaded values (null when absent); an error says why it can't be saved. */
function diffDoc(orig: Doc, next: Doc): Omit<DocEdit, "id" | "text"> | { error: string } {
  if (JSON.stringify(orig._id) !== JSON.stringify(next._id)) return { error: "The _id can't change: add a new document and delete this one." };
  const set: Record<string, string> = {};
  const unset: string[] = [];
  const was: Record<string, string | null> = {};
  for (const [k, v] of Object.entries(next))
    if (!(k in orig) || JSON.stringify(orig[k]) !== JSON.stringify(v)) {
      set[k] = JSON.stringify(v);
      was[k] = k in orig ? JSON.stringify(orig[k]) : null;
    }
  for (const k of Object.keys(orig))
    if (!(k in next)) {
      unset.push(k);
      was[k] = JSON.stringify(orig[k]);
    }
  const odd = Object.keys(was).find((k) => k.startsWith("$") || k.includes("."));
  if (odd) return { error: `Field ${odd} can't be edited here: change it from the console.` };
  return { set, unset, was };
}

/** A collection's documents, a page at a time, filtered and sorted; edited as JSON once writes are allowed. */
export function CollectionView({
  serviceId,
  database,
  collection,
  collections,
  onPick,
  sidebar,
  sidebarOpen,
  onToggleSidebar,
  write: writeAllowed,
  writeSwitch,
  onDropped,
}: {
  serviceId: string;
  database: string;
  collection: PgTable;
  collections: PgTable[];
  onPick: (name: string) => void;
  sidebar: ReactNode;
  sidebarOpen: boolean;
  onToggleSidebar: () => void;
  write: boolean;
  /** Read only / Allow writes, just before Reload. */
  writeSwitch: ReactNode;
  onDropped: () => void;
}) {
  const [size, setSize] = useState(25);
  const [page, setPage] = useState(0);
  // Applied on Enter: half-typed JSON never reaches the server.
  const [filter, setFilter] = useState("");
  const [sort, setSort] = useState("");
  const q = { filter, sort, limit: size, skip: page * size };
  const docs = useQuery({
    queryKey: ["data", serviceId, "documents", database, collection.name, q],
    queryFn: () => api.mongoDocuments(serviceId, database, collection.name, q),
    placeholderData: keepPreviousData,
  });
  const data = docs.data;
  const pages = !filter && collection.rowEstimate > 0 ? Math.max(1, Math.ceil(collection.rowEstimate / size)) : null;
  const queryClient = useQueryClient();
  const { confirm } = useUnsaved();
  const { pending, setPending, counts } = usePending(nothing, writeAllowed);
  const [reviewing, setReviewing] = useState(false);
  // The document in the editor: an existing one by _id, or a new one (insert: its place once staged).
  const [editing, setEditing] = useState<{ id?: string; insert?: number; text: string } | null>(null);
  const seq = useRef(0);
  const guard = (fn: () => void) =>
    confirm(() => {
      setPending(nothing);
      fn();
    });
  const lockedWhy = collection.kind !== "collection" ? `A ${collection.kind}'s documents can't be edited here: use the console.` : undefined;
  const write = writeAllowed && !lockedWhy;
  const changes = (): DocChanges => ({
    deletes: Object.values(pending.deletes).map((id) => ({ id })),
    updates: Object.entries(pending.edits)
      .filter(([k]) => !pending.deletes[k])
      .map(([, { id, set, unset, was }]) => ({ id, set, unset, was })),
    inserts: pending.inserts.map((ins) => JSON.stringify(JSON.parse(ins.text))).reverse(),
  });
  /** Stages the editor's text: a new document as is, an existing one as its changed fields. */
  const stage = (text: string): string | undefined => {
    const next = parseDoc(text);
    if (!next) return "This isn't a JSON object.";
    if (!editing) return;
    if (editing.id === undefined) {
      const i = editing.insert;
      setPending((p) => ({
        ...p,
        inserts: i === undefined ? [{ id: ++seq.current, text }, ...p.inserts] : p.inserts.map((ins, k) => (k === i ? { ...ins, text } : ins)),
      }));
      return;
    }
    const id = editing.id;
    const orig = data?.documents.map(parseDoc).find((d) => d && JSON.stringify(d._id) === id);
    if (!orig) return "This document is no longer on the page: reload.";
    const diff = diffDoc(orig, next);
    if ("error" in diff) return diff.error;
    setPending((p) => {
      const edits = { ...p.edits };
      if (Object.keys(diff.set).length + diff.unset.length) edits[id] = { id, text, ...diff };
      else delete edits[id];
      return { ...p, edits };
    });
  };

  return (
    <BrowserShell
      sidebar={sidebar}
      sidebarOpen={sidebarOpen}
      toolbar={
        <>
          <SidebarToggle open={sidebarOpen} onToggle={onToggleSidebar} />
          <Select value={collection.name} onValueChange={onPick}>
            <SelectTrigger size="sm" aria-label="Collection" className="font-mono text-xs md:hidden">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {collections.map((c) => (
                <SelectItem key={c.name} value={c.name} className="font-mono text-xs">
                  {c.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <JsonField
            icon={<ListFilter />}
            label="Filter"
            placeholder='{ "status": "active" }'
            value={filter}
            onApply={(v) =>
              guard(() => {
                setPage(0);
                setFilter(v);
              })
            }
            className="min-w-40 flex-1"
          />
          <JsonField
            icon={<ArrowDownUp />}
            label="Sort"
            placeholder='{ "_id": -1 }'
            value={sort}
            onApply={(v) =>
              guard(() => {
                setPage(0);
                setSort(v);
              })
            }
            className="w-36 sm:w-40"
          />
          {write && (
            <Button variant="outline" size="sm" onClick={() => setEditing({ text: "{\n  \n}" })}>
              <Plus /> Document
            </Button>
          )}
          {writeAllowed && (
            <DropButton
              what={collection.kind}
              name={collection.name}
              run={(apply) => api.dropCollection(serviceId, database, collection.name, apply)}
              onDropped={onDropped}
              note={
                collection.kind === "collection"
                  ? "Its documents and indexes go with it, and only a restore from a backup brings them back."
                  : "Only its definition goes: the documents it shows stay where they are."
              }
            />
          )}
          {writeSwitch}
          <Button variant="outline" size="icon-sm" title="Reload documents" onClick={() => docs.refetch()}>
            <RefreshCw className={cn(docs.isFetching && "animate-spin")} />
          </Button>
        </>
      }
      footer={
        <>
          <Pager
            page={page}
            pages={pages}
            size={size}
            sizes={PAGE_SIZES}
            unit="documents"
            hasMore={data?.hasMore}
            onPage={(n) => guard(() => setPage(n))}
            onSize={(n) =>
              guard(() => {
                setPage(0);
                setSize(n);
              })
            }
          />
          <ErrorText error={docs.error} />
          {writeAllowed && lockedWhy && <span className="text-xs text-muted-foreground">{lockedWhy}</span>}
          {write && (
            <ChangeBar
              counts={counts}
              onDiscard={() => setPending(nothing)}
              onSave={() => setReviewing(true)}
            />
          )}
          <ReviewDialog
            open={reviewing}
            onOpenChange={setReviewing}
            atomic="Every update and delete is checked first, then they run in order. MongoDB has no transaction on a single server: if one fails, those before it stay saved."
            run={(apply) => api.docChanges(serviceId, database, collection.name, changes(), apply)}
            onSaved={(res) => {
              setPending(nothing);
              toast.success(res.statements.length === 1 ? "1 change saved" : `${res.statements.length} changes saved`);
              queryClient.invalidateQueries({ queryKey: ["data", serviceId, "documents", database, collection.name] });
              queryClient.invalidateQueries({ queryKey: ["data", serviceId, "tables", database] });
            }}
            onFailed={() => queryClient.invalidateQueries({ queryKey: ["data", serviceId, "documents", database, collection.name] })}
          />
          <DocEditor
            key={editing ? `${editing.id ?? "new"}:${editing.insert ?? ""}` : "closed"}
            editing={editing}
            onClose={() => setEditing(null)}
            onDone={stage}
          />
        </>
      }
      write={write}
    >
      {docs.isPending ? (
        <Loading />
      ) : data && data.documents.length === 0 && !(write && pending.inserts.length) ? (
        <p className="p-6 text-center text-sm text-muted-foreground">{filter ? "No document matches." : "This collection is empty."}</p>
      ) : (
        data && (
          <ol className="divide-y" aria-label="Documents">
            {write &&
              pending.inserts.map((ins, i) => (
                <Document
                  key={`new-${ins.id}`}
                  json={ins.text}
                  cut={false}
                  state="new"
                  onEdit={() => setEditing({ insert: i, text: ins.text })}
                  onDelete={() => setPending((p) => ({ ...p, inserts: p.inserts.filter((_, k) => k !== i) }))}
                />
              ))}
            {data.documents.map((d, i) => {
              const cut = data.truncated.includes(i);
              const parsed = cut ? null : parseDoc(d);
              const id = parsed && "_id" in parsed ? JSON.stringify(parsed._id) : undefined;
              const e = id === undefined ? undefined : pending.edits[id];
              return (
                <Document
                  key={page * size + i}
                  json={write && e ? e.text : d}
                  cut={cut}
                  state={!write || id === undefined ? undefined : pending.deletes[id] ? "deleted" : e ? "edited" : undefined}
                  changed={e && [...Object.keys(e.set), ...e.unset]}
                  locked={write && id === undefined ? "Cut at 4 KiB or without an _id: change it from the console." : undefined}
                  onEdit={write && id !== undefined ? () => setEditing({ id, text: e?.text ?? JSON.stringify(parsed, null, 2) }) : undefined}
                  onDelete={
                    write && id !== undefined
                      ? () =>
                          setPending((p) => {
                            const deletes = { ...p.deletes };
                            if (deletes[id]) delete deletes[id];
                            else deletes[id] = id;
                            return { ...p, deletes };
                          })
                      : undefined
                  }
                />
              );
            })}
          </ol>
        )
      )}
    </BrowserShell>
  );
}
