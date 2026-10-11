import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, ChevronLeft, ChevronRight, ChevronsLeft, Download, ListFilter, Plus, RefreshCw, X } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { toast } from "sonner";
import { ErrorText, Loading } from "@/components/common";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue } from "@/components/ui/select";
import { cn } from "@/lib/utils";
import { api, type PgColumn, type PgFilter, type PgFilterOp, type PgTable, type RowChanges } from "../../api";
import { BrowserShell, SearchBox, SidebarHeader, SidebarItem, SidebarToggle } from "./browser-shell";
import { ChangeBar, ReviewDialog, useUnsaved } from "./changes";
import { CELL_MAX, DataTable, type Cell, type GridEdit } from "./data-table";
import { DropButton, NewTableDialog } from "./schema-dialogs";
import type { SqlDialect } from "./sql-editor";
import { WriteMode } from "./write-mode";

const PAGE_SIZES = [25, 50, 100, 200];

const opGroups: { label: string; ops: { op: PgFilterOp; label: string }[] }[] = [
  {
    label: "Comparison",
    ops: [
      { op: "=", label: "=" },
      { op: "!=", label: "!=" },
      { op: "<", label: "<" },
      { op: "<=", label: "<=" },
      { op: ">", label: ">" },
      { op: ">=", label: ">=" },
    ],
  },
  { label: "Text search", ops: [{ op: "like", label: "ilike" }] },
  {
    label: "Null checks",
    ops: [
      { op: "null", label: "is null" },
      { op: "notnull", label: "is not null" },
    ],
  },
];

const CELL_KIB = CELL_MAX / 1024;

const tableKey = (t: Pick<PgTable, "schema" | "name">) => `${t.schema}.${t.name}`;

/** defaultSchema's tables are listed without a heading: public, or MySQL's database. */
export function PgBrowser({ serviceId, database, defaultSchema, dialect }: { serviceId: string; database: string; defaultSchema: string; dialect: SqlDialect }) {
  const tables = useQuery({ queryKey: ["data", serviceId, "tables", database], queryFn: () => api.pgTables(serviceId, database) });
  const [picked, setPicked] = useState<string | null>(null);
  const [find, setFind] = useState("");
  const [sidebar, setSidebar] = useState(true);
  // Kept across tables: switching table doesn't lock it again.
  const [write, setWrite] = useState(false);
  const [creating, setCreating] = useState(false);
  const { confirm } = useUnsaved();
  const queryClient = useQueryClient();
  const pick = (key: string) => confirm(() => setPicked(key));

  if (tables.isPending) return <Loading />;
  if (tables.error) return <ErrorText error={tables.error} />;
  const list = tables.data ?? [];
  const writeSwitch = (
    <WriteMode
      className="ml-auto"
      write={write}
      onChange={(w) => (w ? setWrite(true) : confirm(() => setWrite(false)))}
      description="You can then edit cells, add and delete rows, and create or drop tables. Nothing is written until you review the statements and confirm; only a restore from a backup undoes it."
    />
  );
  const newTable = (
    <NewTableDialog
      open={creating}
      onOpenChange={setCreating}
      dialect={dialect}
      defaultSchema={defaultSchema}
      run={(schema, name, columns, apply) => api.createTable(serviceId, database, schema, name, columns, apply)}
      onCreated={(schema, name) => {
        queryClient.invalidateQueries({ queryKey: ["data", serviceId, "tables", database] });
        setPicked(tableKey({ schema: dialect === "postgres" ? schema || "public" : database, name }));
      }}
    />
  );
  const newTableButton = write && (
    <Button variant="ghost" size="icon-xs" title="New table" onClick={() => confirm(() => setCreating(true))}>
      <Plus />
    </Button>
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
            <Button variant="outline" size="icon-sm" title="Reload tables" onClick={() => tables.refetch()}>
              <RefreshCw className={cn(tables.isFetching && "animate-spin")} />
            </Button>
            {newTable}
          </>
        }
      >
        <div className="flex flex-col items-center gap-3 p-10 text-center text-sm text-muted-foreground">
          <p>No tables in this database yet. Once your app runs its migrations, its tables show up here.</p>
          {write ? (
            <Button variant="outline" size="sm" onClick={() => setCreating(true)}>
              <Plus /> Table
            </Button>
          ) : (
            <p>Or allow writes to create one.</p>
          )}
        </div>
      </BrowserShell>
    );
  const current = list.find((t) => tableKey(t) === picked) ?? list[0];
  const shown = list.filter((t) => tableKey(t).toLowerCase().includes(find.trim().toLowerCase()));
  const schemas = [...new Set(shown.map((t) => t.schema))];

  const nav = (
    <>
      <SidebarHeader
        title="Tables"
        search={find}
        onSearch={setFind}
        actions={
          <>
            {newTableButton}
            <Button variant="ghost" size="icon-xs" title="Reload tables" onClick={() => tables.refetch()}>
              <RefreshCw className={cn(tables.isFetching && "animate-spin")} />
            </Button>
          </>
        }
      />
      {newTable}
      <nav aria-label="Tables" className="flex-1 space-y-3 overflow-y-auto p-2">
        {shown.length === 0 && <p className="px-2 text-xs text-muted-foreground">No table matches.</p>}
        {schemas.map((schema) => (
          <div key={schema} className="space-y-px">
            {(schemas.length > 1 || schema !== defaultSchema) && <p className="px-2 pb-1 text-xs text-muted-foreground">{schema}</p>}
            {shown
              .filter((t) => t.schema === schema)
              .map((t) => (
                <SidebarItem key={tableKey(t)} selected={tableKey(t) === tableKey(current)} onClick={() => pick(tableKey(t))} title={t.kind}>
                  <span className="min-w-0 flex-1 truncate">{t.name}</span>
                  {t.kind === "view" && <span className="text-[0.65rem] opacity-70">view</span>}
                  {t.kind === "materialized view" && <span className="text-[0.65rem] opacity-70">mview</span>}
                </SidebarItem>
              ))}
          </div>
        ))}
      </nav>
    </>
  );

  return (
    <TableView
      key={tableKey(current)}
      serviceId={serviceId}
      database={database}
      table={current}
      tables={list}
      onPick={pick}
      sidebar={nav}
      sidebarOpen={sidebar}
      onToggleSidebar={() => setSidebar(!sidebar)}
      dialect={dialect}
      write={write}
      writeSwitch={writeSwitch}
      onDropped={() => {
        setPicked(null);
        queryClient.invalidateQueries({ queryKey: ["data", serviceId, "tables", database] });
      }}
    />
  );
}

/** Edits waiting for Save, rows found by their primary key (as JSON of its cells). */
type Pending = {
  edits: Record<string, { key: Record<string, string>; set: Record<string, string | null>; was: Record<string, string | null> }>;
  deletes: Record<string, Record<string, string>>;
  /** Newest first, as shown. */
  inserts: { id: number; values: Record<string, string | null> }[];
};

const nothing: Pending = { edits: {}, deletes: {}, inserts: [] };

/** MySQL columns of bytes, shown as hex: edited from the console only. */
const mysqlBinaryRe = /^((tiny|medium|long)?blob|(var)?binary|bit|geometry|point|linestring|polygon|multipoint|multilinestring|multipolygon|geometrycollection)\b/i;

function TableView({
  serviceId,
  database,
  table,
  tables,
  onPick,
  sidebar,
  sidebarOpen,
  onToggleSidebar,
  dialect,
  write: writeAllowed,
  writeSwitch,
  onDropped,
}: {
  serviceId: string;
  database: string;
  table: PgTable;
  tables: PgTable[];
  onPick: (key: string) => void;
  sidebar: ReactNode;
  sidebarOpen: boolean;
  onToggleSidebar: () => void;
  dialect: SqlDialect;
  write: boolean;
  /** Read only / Allow writes, just before Reload. */
  writeSwitch: ReactNode;
  onDropped: () => void;
}) {
  const [size, setSize] = useState(25);
  const [page, setPage] = useState(0);
  const [sort, setSort] = useState<{ column: string; desc: boolean } | undefined>();
  const [filters, setFilters] = useState<PgFilter[]>([]);
  const [search, setSearch] = useState("");
  const q = { limit: size, offset: page * size, orderBy: sort?.column, desc: sort?.desc, filters, search };
  const rows = useQuery({
    queryKey: ["data", serviceId, "rows", database, table.schema, table.name, q],
    queryFn: () => api.pgRows(serviceId, database, table.schema, table.name, q),
    placeholderData: keepPreviousData,
  });
  const data = rows.data;
  const cols: PgColumn[] = data?.columns ?? table.columns;
  const columns = cols.map((c) => ({ name: c.name, type: c.type, primaryKey: c.primaryKey, required: !c.nullable && !c.primaryKey }));
  const pk = cols.flatMap((c, j) => (c.primaryKey ? [j] : []));
  // From the planner's estimate: rough, and unknown while searching or filtering.
  const pages = !filters.length && !search && table.rowEstimate > 0 ? Math.max(1, Math.ceil(table.rowEstimate / size)) : null;
  const queryClient = useQueryClient();
  const { setDirty, confirm } = useUnsaved();
  const [pending, setPending] = useState<Pending>(nothing);
  const [reviewing, setReviewing] = useState(false);
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
  /** Runs a change of view once the pending edits are saved or dropped. */
  const guard = (fn: () => void) =>
    confirm(() => {
      setPending(nothing);
      fn();
    });

  const lockedWhy =
    table.kind !== "table" && table.kind !== "partitioned table"
      ? `A ${table.kind}'s rows can't be edited here: use the console.`
      : pk.length === 0
        ? "This table has no primary key to find its rows again: change them from the console."
        : undefined;
  const write = writeAllowed && !lockedWhy;
  const loaded = data?.rows ?? [];
  const cut = new Set((data?.truncated ?? []).map(([r, c]) => `${r}:${c}`));
  const keyOf = (r: (string | null)[]) => JSON.stringify(pk.map((j) => r[j]));
  const nNew = write ? pending.inserts.length : 0;
  const binary = (j: number) => dialect !== "postgres" && mysqlBinaryRe.test(cols[j].type);
  const shown: Cell[][] = write
    ? [
        ...pending.inserts.map((ins) => cols.map((c) => (c.name in ins.values ? ins.values[c.name] : undefined))),
        ...loaded.map((r) => {
          const e = pending.edits[keyOf(r)];
          return e ? cols.map((c, j) => (c.name in e.set ? e.set[c.name] : r[j])) : r;
        }),
      ]
    : loaded;
  const edit: GridEdit | undefined = write
    ? {
        nulls: true,
        state: (i) => (i < nNew ? "new" : pending.deletes[keyOf(loaded[i - nNew])] ? "deleted" : undefined),
        was: (i, j) => {
          if (i < nNew) return undefined;
          const r = loaded[i - nNew];
          const e = pending.edits[keyOf(r)];
          return e && cols[j].name in e.set ? { value: e.was[cols[j].name] } : undefined;
        },
        locked: (i, j) => {
          if (j >= 0 && binary(j)) return "Bytes, shown as hex: change them from the console.";
          if (i < nNew) return undefined;
          const r = i - nNew;
          if (pk.some((k) => cut.has(`${r}:${k}`))) return "This row's key is too long to find it again: use the console.";
          if (j >= 0 && cut.has(`${r}:${j}`) && !(cols[j].name in (pending.edits[keyOf(loaded[r])]?.set ?? {})))
            return `Only the first ${CELL_KIB} KiB are loaded: change it from the console.`;
          return undefined;
        },
        onEdit: (i, j, v) => {
          const col = cols[j].name;
          if (i < nNew) {
            setPending((p) => ({
              ...p,
              inserts: p.inserts.map((ins, k) => {
                if (k !== i) return ins;
                const values = { ...ins.values };
                if (v === undefined) delete values[col];
                else values[col] = v;
                return { ...ins, values };
              }),
            }));
            return;
          }
          if (v === undefined) return;
          const r = loaded[i - nNew];
          const k = keyOf(r);
          setPending((p) => {
            const e = p.edits[k] ?? { key: Object.fromEntries(pk.map((x) => [cols[x].name, r[x] ?? ""])), set: {}, was: {} };
            const set = { ...e.set };
            const was = { ...e.was };
            if (v === r[j]) {
              delete set[col];
              delete was[col];
            } else {
              set[col] = v;
              was[col] = r[j];
            }
            const edits = { ...p.edits };
            if (Object.keys(set).length) edits[k] = { ...e, set, was };
            else delete edits[k];
            return { ...p, edits };
          });
        },
        onDelete: (i) => {
          if (i < nNew) {
            setPending((p) => ({ ...p, inserts: p.inserts.filter((_, k) => k !== i) }));
            return;
          }
          const r = loaded[i - nNew];
          const k = keyOf(r);
          setPending((p) => {
            const deletes = { ...p.deletes };
            if (deletes[k]) delete deletes[k];
            else deletes[k] = Object.fromEntries(pk.map((x) => [cols[x].name, r[x] ?? ""]));
            return { ...p, deletes };
          });
        },
      }
    : undefined;
  const changes = (): RowChanges => ({
    deletes: Object.values(pending.deletes).map((key) => ({ key })),
    updates: Object.entries(pending.edits)
      .filter(([k]) => !pending.deletes[k])
      .map(([, e]) => e),
    // Oldest first: ids follow the order rows were added.
    inserts: pending.inserts.map((ins) => ins.values).reverse(),
  });

  const filterBar = useFilters(cols, (f) =>
    guard(() => {
      setPage(0);
      setFilters(f);
    }),
  );

  return (
    <BrowserShell
      sidebar={sidebar}
      sidebarOpen={sidebarOpen}
      toolbar={
        <>
          <SidebarToggle open={sidebarOpen} onToggle={onToggleSidebar} />
          <Select value={tableKey(table)} onValueChange={onPick}>
            <SelectTrigger size="sm" aria-label="Table" className="font-mono text-xs md:hidden">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {tables.map((t) => (
                <SelectItem key={tableKey(t)} value={tableKey(t)} className="font-mono text-xs">
                  {tableKey(t)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <SearchBox
            value={search}
            onChange={(s) =>
              guard(() => {
                setPage(0);
                setSearch(s);
              })
            }
            placeholder="Search rows"
            className="w-44 sm:w-56"
          />
          {filterBar.trigger}
          <Button variant="outline" size="icon-sm" asChild>
            <a href={api.pgExportUrl(serviceId, database, table.schema, table.name, q)} download title="Export the rows matching the search and filters as CSV" aria-label="Export CSV">
              <Download />
            </a>
          </Button>
          {write && (
            <Button
              variant="outline"
              size="sm"
              onClick={() => setPending((p) => ({ ...p, inserts: [{ id: ++seq.current, values: {} }, ...p.inserts] }))}
            >
              <Plus /> Row
            </Button>
          )}
          {writeAllowed && (
            <DropButton
              what={table.kind === "partitioned table" ? "table" : table.kind}
              name={table.name}
              run={(apply) => api.dropTable(serviceId, database, table.schema, table.name, apply)}
              onDropped={onDropped}
              note={`Its ${table.kind.endsWith("view") ? "definition" : "rows"} go with it, and only a restore from a backup brings them back. If a view or a foreign key depends on it, the database refuses.`}
            />
          )}
          {writeSwitch}
          <Button variant="outline" size="icon-sm" title="Reload rows" onClick={() => rows.refetch()}>
            <RefreshCw className={cn(rows.isFetching && "animate-spin")} />
          </Button>
          {filterBar.pills}
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
              {pages && <span className="ml-1 text-muted-foreground">of ~{Intl.NumberFormat().format(Math.max(pages, page + 1))}</span>}
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
              <SelectTrigger size="sm" aria-label="Rows per page" className="h-auto! rounded-none border-0 border-l shadow-none">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {PAGE_SIZES.map((n) => (
                  <SelectItem key={n} value={String(n)}>
                    {n} rows per page
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Button variant="ghost" size="icon-sm" className="h-auto rounded-none border-l" title="Next page" disabled={!data?.hasMore} onClick={() => guard(() => setPage(page + 1))}>
              <ChevronRight />
            </Button>
          </div>
          <ErrorText error={rows.error} />
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
            run={(apply) => api.rowChanges(serviceId, database, table.schema, table.name, changes(), apply)}
            onSaved={(res) => {
              setPending(nothing);
              toast.success(res.statements.length === 1 ? "1 change saved" : `${res.statements.length} changes saved`);
              queryClient.invalidateQueries({ queryKey: ["data", serviceId, "rows", database, table.schema, table.name] });
              queryClient.invalidateQueries({ queryKey: ["data", serviceId, "tables", database] });
            }}
          />
        </>
      }
      write={write}
    >
      {rows.isPending ? (
        <Loading />
      ) : (
        data && (
          <DataTable
            className="max-h-none rounded-none border-0"
            columns={columns}
            rows={shown}
            truncated={data.truncated.map(([r, c]): [number, number] => [r + nNew, c])}
            sort={sort}
            onSort={(column) =>
              guard(() => {
                setPage(0);
                setSort((s) => (s?.column !== column ? { column, desc: false } : s.desc ? undefined : { column, desc: true }));
              })
            }
            rowLabel={(r, i) =>
              i < nNew ? "New row" : pk.length ? pk.map((j) => `${cols[j].name} ${r[j]}`).join(", ") : `Row ${page * size + i + 1}`
            }
            empty={filters.length || search ? "No row matches." : "This table is empty."}
            edit={edit}
          />
        )
      )}
    </BrowserShell>
  );
}

type Draft = PgFilter & { id: number; applied: boolean };

const isUnary = (op: PgFilterOp) => op === "null" || op === "notnull";
const ready = (f: PgFilter) => isUnary(f.op) || (f.value ?? "") !== "";
const strip = ({ column, op, value }: PgFilter): PgFilter => (isUnary(op) ? { column, op } : { column, op, value });

/**
 * Filters as pills: pick a column from the menu, then an operator and a
 * value; ✓ (or Enter) applies it. Only applied, complete filters reach the
 * server, so removing an unfinished one changes nothing.
 */
function useFilters(columns: PgColumn[], onChange: (f: PgFilter[]) => void) {
  const seq = useRef(0);
  const focusNext = useRef<number | null>(null);
  const [draft, setDraft] = useState<Draft[]>([]);
  const publish = (next: Draft[]) => {
    setDraft(next);
    onChange(next.filter((d) => d.applied && ready(d)).map(strip));
  };
  const edit = (id: number, f: Partial<PgFilter>) => setDraft(draft.map((d) => (d.id === id ? { ...d, ...f, applied: false } : d)));
  const remove = (id: number) => {
    const next = draft.filter((d) => d.id !== id);
    if (draft.find((d) => d.id === id)?.applied) publish(next);
    else setDraft(next);
  };

  const trigger = (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm">
          <ListFilter /> Filter
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        align="start"
        className="max-h-80 w-64 overflow-y-auto"
        onCloseAutoFocus={(e) => {
          // Focus goes to the new filter's value, not back to this button.
          if (focusNext.current === null) return;
          e.preventDefault();
          document.getElementById(`filter-${focusNext.current}`)?.focus();
          focusNext.current = null;
        }}
      >
        {columns.map((c) => (
          <DropdownMenuItem
            key={c.name}
            className="font-mono text-xs"
            onSelect={() => {
              focusNext.current = ++seq.current;
              setDraft([...draft, { id: seq.current, column: c.name, op: "=", value: "", applied: false }]);
            }}
          >
            <span className="flex-1 truncate">{c.name}</span>
            <span className="text-muted-foreground">{c.type}</span>
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );

  const pills = draft.length > 0 && (
    <div className="flex w-full flex-wrap gap-1.5 pt-0.5">
      {draft.map((f) => (
        <form
          key={f.id}
          onSubmit={(e) => {
            e.preventDefault();
            publish(draft.map((d) => (d.id === f.id ? { ...d, applied: ready(d) } : d)));
          }}
          className={cn("flex h-7 items-stretch overflow-hidden rounded-md border font-mono text-xs", !f.applied && "border-dashed")}
        >
          <span className="flex items-center bg-muted/50 px-2 font-medium" title={columns.find((c) => c.name === f.column)?.type}>
            {f.column}
          </span>
          <Select value={f.op} onValueChange={(op) => edit(f.id, { op: op as PgFilterOp })}>
            <SelectTrigger size="sm" aria-label="Operator" className="h-auto! rounded-none border-0 border-l px-2 font-mono text-xs shadow-none">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {opGroups.map((g) => (
                <SelectGroup key={g.label}>
                  <SelectLabel className="text-xs">{g.label}</SelectLabel>
                  {g.ops.map((o) => (
                    <SelectItem key={o.op} value={o.op} className="font-mono text-xs">
                      {o.label}
                    </SelectItem>
                  ))}
                </SelectGroup>
              ))}
            </SelectContent>
          </Select>
          {!isUnary(f.op) && (
            <input
              id={`filter-${f.id}`}
              aria-label={`Value for ${f.column}`}
              value={f.value ?? ""}
              placeholder={f.op === "like" ? "%pattern%" : "value"}
              onChange={(e) => edit(f.id, { value: e.target.value })}
              className="w-32 border-l bg-transparent px-2 outline-none placeholder:text-muted-foreground"
            />
          )}
          <button
            type="submit"
            title="Apply filter"
            disabled={f.applied || !ready(f)}
            className="flex items-center border-l px-1.5 text-muted-foreground enabled:hover:bg-muted enabled:hover:text-foreground disabled:opacity-40"
          >
            <Check className="size-3.5" />
          </button>
          <button type="button" title="Remove filter" onClick={() => remove(f.id)} className="flex items-center border-l px-1.5 text-muted-foreground hover:bg-muted hover:text-foreground">
            <X className="size-3.5" />
          </button>
        </form>
      ))}
    </div>
  );

  return { trigger, pills };
}
