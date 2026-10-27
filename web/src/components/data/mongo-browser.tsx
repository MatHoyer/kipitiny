import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDownUp, ChevronLeft, ChevronRight, ChevronsLeft, ListFilter, Pencil, Plus, RefreshCw, Trash2, Undo2 } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { toast } from "sonner";
import { CopyButton, ErrorText, Loading } from "@/components/common";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { cn } from "@/lib/utils";
import { api, type DocChanges, type PgTable } from "../../api";
import { BrowserShell, SidebarNav, SidebarHeader, SidebarItem, SidebarToggle } from "./browser-shell";
import { ChangeBar, ReviewDialog, useUnsaved } from "./changes";
import { DropButton, NewCollectionDialog } from "./schema-dialogs";
import { WriteMode } from "./write-mode";

const PAGE_SIZES = [10, 25, 50, 100];

/** A MongoDB database's collections on the left, the picked one's documents as JSON. */
export function MongoBrowser({ serviceId, database }: { serviceId: string; database: string }) {
  const collections = useQuery({ queryKey: ["data", serviceId, "tables", database], queryFn: () => api.pgTables(serviceId, database) });
  const [picked, setPicked] = useState<string | null>(null);
  const [find, setFind] = useState("");
  const [sidebar, setSidebar] = useState(true);
  const navScroll = useRef(0);
  // Kept across collections: switching doesn't lock it again.
  const [write, setWrite] = useState(false);
  const [creating, setCreating] = useState(false);
  const { confirm } = useUnsaved();
  const queryClient = useQueryClient();
  const pick = (name: string) => confirm(() => setPicked(name));

  if (collections.isPending) return <Loading />;
  if (collections.error) return <ErrorText error={collections.error} />;
  const list = collections.data ?? [];
  const writeSwitch = (
    <WriteMode
      className="ml-auto"
      write={write}
      onChange={(w) => (w ? setWrite(true) : confirm(() => setWrite(false)))}
      description="You can then edit, add and delete documents, and create or drop collections. Nothing is written until you review the calls and confirm; only a restore from a backup undoes it."
    />
  );
  const newCollection = (
    <NewCollectionDialog
      open={creating}
      onOpenChange={setCreating}
      run={(name, apply) => api.createCollection(serviceId, database, name, apply)}
      onCreated={(name) => {
        queryClient.invalidateQueries({ queryKey: ["data", serviceId, "tables", database] });
        setPicked(name);
      }}
    />
  );
  if (list.length === 0)
    return (
      <BrowserShell
        write={write}
        sidebarOpen={false}
        sidebar={null}
        toolbar={
          <>
            {writeSwitch}
            <Button variant="outline" size="icon-sm" title="Reload collections" onClick={() => collections.refetch()}>
              <RefreshCw className={cn(collections.isFetching && "animate-spin")} />
            </Button>
            {newCollection}
          </>
        }
      >
        <div className="flex flex-col items-center gap-3 p-10 text-center text-sm text-muted-foreground">
          <p>No collections in this database yet. They show up here once your app writes to them.</p>
          {write ? (
            <Button variant="outline" size="sm" onClick={() => setCreating(true)}>
              <Plus /> Collection
            </Button>
          ) : (
            <p>Or allow writes to create one.</p>
          )}
        </div>
      </BrowserShell>
    );
  const current = list.find((c) => c.name === picked) ?? list[0];
  const shown = list.filter((c) => c.name.toLowerCase().includes(find.trim().toLowerCase()));

  const nav = (
    <>
      <SidebarHeader
        title="Collections"
        search={find}
        onSearch={setFind}
        actions={
          <>
            {write && (
              <Button variant="ghost" size="icon-xs" title="New collection" onClick={() => confirm(() => setCreating(true))}>
                <Plus />
              </Button>
            )}
            <Button variant="ghost" size="icon-xs" title="Reload collections" onClick={() => collections.refetch()}>
              <RefreshCw className={cn(collections.isFetching && "animate-spin")} />
            </Button>
          </>
        }
      />
      {newCollection}
      <SidebarNav scroll={navScroll} aria-label="Collections" className="flex-1 space-y-px overflow-y-auto p-2">
        {shown.length === 0 && <p className="px-2 text-xs text-muted-foreground">No collection matches.</p>}
        {shown.map((c) => (
          <SidebarItem key={c.name} selected={c.name === current.name} onClick={() => pick(c.name)} title={c.kind}>
            <span className="min-w-0 flex-1 truncate">{c.name}</span>
            {c.kind !== "collection" && <span className="text-[0.65rem] opacity-70">{c.kind}</span>}
          </SidebarItem>
        ))}
      </SidebarNav>
    </>
  );

  return (
    <CollectionView
      key={current.name}
      serviceId={serviceId}
      database={database}
      collection={current}
      collections={list}
      onPick={pick}
      sidebar={nav}
      sidebarOpen={sidebar}
      onToggleSidebar={() => setSidebar(!sidebar)}
      write={write}
      writeSwitch={writeSwitch}
      onDropped={() => {
        setPicked(null);
        queryClient.invalidateQueries({ queryKey: ["data", serviceId, "tables", database] });
      }}
    />
  );
}

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

function CollectionView({
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
  const { setDirty, confirm } = useUnsaved();
  const [pending, setPending] = useState<Pending>(nothing);
  const [reviewing, setReviewing] = useState(false);
  // The document in the editor: an existing one by _id, or a new one (insert: its place once staged).
  const [editing, setEditing] = useState<{ id?: string; insert?: number; text: string } | null>(null);
  const seq = useRef(0);
  const edited = Object.keys(pending.edits).filter((k) => !pending.deletes[k]).length;
  const deleted = Object.keys(pending.deletes).length;
  const dirty = edited + deleted + pending.inserts.length;
  useEffect(() => setDirty(dirty), [dirty, setDirty]);
  useEffect(() => () => setDirty(0), [setDirty]);
  // Turning writes off (once agreed to) drops what was pending.
  useEffect(() => {
    if (!writeAllowed) setPending(nothing);
  }, [writeAllowed]);
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
          <div className="flex h-8 items-stretch overflow-hidden rounded-md border">
            <Button variant="ghost" size="icon-sm" className="h-auto rounded-none" title="First page" disabled={page === 0} onClick={() => guard(() => setPage(0))}>
              <ChevronsLeft />
            </Button>
            <Button variant="ghost" size="icon-sm" className="h-auto rounded-none border-l" title="Previous page" disabled={page === 0} onClick={() => guard(() => setPage(page - 1))}>
              <ChevronLeft />
            </Button>
            <span className="flex items-center border-l px-3 text-sm whitespace-nowrap tabular-nums">
              {page + 1}
              {pages && <span className="ml-1 text-muted-foreground">of {Intl.NumberFormat().format(Math.max(pages, page + 1))}</span>}
            </span>
            <Select
              value={String(size)}
              onValueChange={(v) =>
                guard(() => {
                  setPage(0);
                  setSize(Number(v));
                })
              }
            >
              <SelectTrigger size="sm" aria-label="Documents per page" className="h-auto! rounded-none border-0 border-l shadow-none">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {PAGE_SIZES.map((n) => (
                  <SelectItem key={n} value={String(n)}>
                    {n} documents per page
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Button variant="ghost" size="icon-sm" className="h-auto rounded-none border-l" title="Next page" disabled={!data?.hasMore} onClick={() => guard(() => setPage(page + 1))}>
              <ChevronRight />
            </Button>
          </div>
          <ErrorText error={docs.error} />
          {writeAllowed && lockedWhy && <span className="text-xs text-muted-foreground">{lockedWhy}</span>}
          {write && (
            <ChangeBar
              counts={[
                { n: edited, label: "edited", tone: "edit" },
                { n: pending.inserts.length, label: "new", tone: "new" },
                { n: deleted, label: "to delete", tone: "delete" },
              ]}
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

/** A filter or sort as Extended JSON, applied on Enter. */
function JsonField({
  icon,
  label,
  placeholder,
  value,
  onApply,
  className,
}: {
  icon: ReactNode;
  label: string;
  placeholder: string;
  value: string;
  onApply: (v: string) => void;
  className?: string;
}) {
  const [text, setText] = useState(value);
  const dirty = text.trim() !== value;
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onApply(text.trim());
      }}
      className={cn(
        "flex h-8 items-center gap-1.5 rounded-md border bg-background px-2 focus-within:border-ring focus-within:ring-[3px] focus-within:ring-ring/50 [&_svg]:size-3.5 [&_svg]:text-muted-foreground",
        dirty && "border-dashed",
        className,
      )}
      title={`${label}: Extended JSON, applied with Enter`}
    >
      {icon}
      <input
        aria-label={label}
        placeholder={placeholder}
        value={text}
        spellCheck={false}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => e.key === "Escape" && setText(value)}
        className="min-w-0 flex-1 bg-transparent font-mono text-xs outline-none placeholder:text-muted-foreground/60"
      />
    </form>
  );
}

/**
 * One document, indented; a cut one stays as the server sent it. While
 * editing, its pending state shows: added, edited (which fields), deleted.
 */
function Document({
  json,
  cut,
  state,
  changed,
  locked,
  onEdit,
  onDelete,
}: {
  json: string;
  cut: boolean;
  state?: "new" | "edited" | "deleted";
  changed?: string[];
  locked?: string;
  onEdit?: () => void;
  onDelete?: () => void;
}) {
  let pretty = json;
  if (!cut) {
    try {
      pretty = JSON.stringify(JSON.parse(json), null, 2);
    } catch {
      // Shown as sent.
    }
  }
  const editable = !!(onEdit || onDelete || locked);
  return (
    <li
      className={cn(
        "group relative px-4 py-3",
        state === "new" && "bg-emerald-500/10 shadow-[inset_2px_0_0] shadow-emerald-500",
        state === "edited" && "bg-amber-500/10 shadow-[inset_2px_0_0] shadow-amber-500",
        state === "deleted" && "bg-destructive/5 text-muted-foreground line-through shadow-[inset_2px_0_0] shadow-destructive",
      )}
    >
      {changed && changed.length > 0 && (
        <p className="mb-1.5 font-mono text-xs text-amber-600 dark:text-amber-400">
          edited: {changed.join(", ")}
        </p>
      )}
      <pre className={cn("overflow-x-auto font-mono text-xs leading-relaxed whitespace-pre-wrap", editable && "pr-24")}>{pretty}</pre>
      {cut && <p className="mt-1 text-xs text-muted-foreground">Cut at 4 KiB: run a projection in the console to see the rest.</p>}
      <span
        className={cn(
          "absolute top-2 right-2 flex items-center transition-opacity",
          !editable && "opacity-0 group-hover:opacity-100 focus-within:opacity-100",
        )}
      >
        {state !== "deleted" && onEdit && (
          <Button variant="ghost" size="icon-xs" title={state === "new" ? "Edit this new document" : "Edit this document"} onClick={onEdit}>
            <Pencil />
          </Button>
        )}
        {onDelete && (
          <Button
            variant="ghost"
            size="icon-xs"
            title={state === "deleted" ? "Keep this document" : state === "new" ? "Remove this new document" : "Delete this document"}
            onClick={onDelete}
            className={cn(state !== "deleted" && "hover:text-destructive")}
          >
            {state === "deleted" ? <Undo2 /> : <Trash2 />}
          </Button>
        )}
        {locked && <span className="px-1 text-xs text-muted-foreground" title={locked}>locked</span>}
        {state !== "new" && <CopyButton value={json} label="Copy as JSON" />}
      </span>
    </li>
  );
}

/** Edits a document as relaxed Extended JSON; Done stages it, the collection's Save writes it. */
function DocEditor({
  editing,
  onClose,
  onDone,
}: {
  editing: { id?: string; insert?: number; text: string } | null;
  onClose: () => void;
  /** Returns why the text can't be staged. */
  onDone: (text: string) => string | undefined;
}) {
  const [text, setText] = useState(editing?.text ?? "");
  const [error, setError] = useState<string>();
  const isNew = editing?.id === undefined;
  const done = () => {
    const why = onDone(text);
    if (why) setError(why);
    else onClose();
  };
  return (
    <Dialog open={!!editing} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="flex max-h-[85vh] flex-col gap-0 p-0 sm:max-w-2xl">
        <DialogHeader className="border-b p-4">
          <DialogTitle className="font-mono text-sm">{isNew ? "New document" : `_id ${editing?.id}`}</DialogTitle>
          <DialogDescription>
            {isNew
              ? "Extended JSON, as shown: { \"$date\": \"…\" } for a date, { \"$oid\": \"…\" } for an ObjectId. Without an _id, MongoDB adds one."
              : "Extended JSON, as shown. Only the top-level fields you change are saved; the others keep their exact types."}
          </DialogDescription>
        </DialogHeader>
        <div className="min-h-0 flex-1 overflow-y-auto p-4">
          <textarea
            autoFocus
            aria-label="Document"
            spellCheck={false}
            value={text}
            onChange={(e) => {
              setText(e.target.value);
              setError(undefined);
            }}
            onKeyDown={(e) => e.key === "Enter" && (e.ctrlKey || e.metaKey) && done()}
            className="field-sizing-content min-h-48 w-full resize-none rounded-md border bg-background p-3 font-mono text-xs leading-relaxed outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
          />
          {error && <p className="mt-2 text-sm text-destructive">{error}</p>}
        </div>
        <DialogFooter className="m-0">
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={done}>
            Done
            <kbd className="ml-1 hidden font-sans text-[0.7rem] opacity-60 sm:inline">Ctrl ↵</kbd>
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
