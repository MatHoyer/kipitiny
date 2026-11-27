import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, Plus, RefreshCw } from "lucide-react";
import { useRef, useState, type ReactNode } from "react";
import { toast } from "sonner";
import { ErrorText } from "@/components/common";
import { Loading } from "@kipit-labs/kipitidy/loading";
import { Button } from "@kipit-labs/kipitidy/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@kipit-labs/kipitidy/select";
import { cn } from "@/lib/utils";
import { api, type PgColumn, type PgFilter, type PgRef, type PgTable, type RowChanges } from "../../api";
import { BrowserShell, SearchBox, SidebarToggle } from "./browser-shell";
import { ChangeBar, ReviewDialog, useUnsaved } from "./changes";
import { CELL_MAX, DataTable, type Cell, type GridEdit } from "./data-table";
import { Pager } from "./pager";
import { usePending } from "./pending";
import { useFilters } from "./pg-filters";
import { DropButton } from "./schema-dialogs";
import type { SqlDialect } from "./sql-editor";
import { tableKey } from "./table-key";

const PAGE_SIZES = [25, 50, 100, 200];

const CELL_KIB = CELL_MAX / 1024;

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
/** SQLite gives BLOB affinity to a declared type naming it. */
const sqliteBinaryRe = /blob/i;

/** A table's rows, a page at a time, searched, filtered and sorted; edited in place once writes are allowed. */
export function TableView({
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
  initialFilters = [],
  onFollow,
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
  initialFilters?: PgFilter[];
  onFollow: (ref: PgRef, value: string) => void;
}) {
  const [size, setSize] = useState(25);
  const [page, setPage] = useState(0);
  const [sort, setSort] = useState<{ column: string; desc: boolean } | undefined>();
  const [filters, setFilters] = useState(initialFilters);
  const [search, setSearch] = useState("");
  const q = { limit: size, offset: page * size, orderBy: sort?.column, desc: sort?.desc, filters, search };
  const rows = useQuery({
    queryKey: ["data", serviceId, "rows", database, table.schema, table.name, q],
    queryFn: () => api.pgRows(serviceId, database, table.schema, table.name, q),
    placeholderData: keepPreviousData,
  });
  const data = rows.data;
  const cols: PgColumn[] = data?.columns ?? table.columns;
  // Only the table listing knows the foreign keys.
  const refKey = (r: PgRef) => tableKey({ schema: r.schema, name: r.table });
  const refs = new Map<string, PgRef>();
  for (const c of table.columns) if (c.references && tables.some((t) => tableKey(t) === refKey(c.references!))) refs.set(c.name, c.references);
  const columns = cols.map((c) => {
    const ref = refs.get(c.name);
    return { name: c.name, type: c.type, primaryKey: c.primaryKey, required: !c.nullable && !c.primaryKey, references: ref && `${refKey(ref)}.${ref.column}` };
  });
  const pk = cols.flatMap((c, j) => (c.primaryKey ? [j] : []));
  // From the planner's estimate: rough, and unknown while searching or filtering.
  const pages = !filters.length && !search && table.rowEstimate > 0 ? Math.max(1, Math.ceil(table.rowEstimate / size)) : null;
  const queryClient = useQueryClient();
  const { confirm } = useUnsaved();
  const { pending, setPending, counts } = usePending(nothing, writeAllowed);
  const [reviewing, setReviewing] = useState(false);
  const seq = useRef(0);
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
  const binary = (j: number) => dialect !== "postgres" && (dialect === "sqlite" ? sqliteBinaryRe : mysqlBinaryRe).test(cols[j].type);
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

  const filterBar = useFilters(cols, initialFilters, (f) =>
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
          <Pager
            page={page}
            pages={pages}
            approx
            size={size}
            sizes={PAGE_SIZES}
            unit="rows"
            hasMore={data?.hasMore}
            onPage={(n) => guard(() => setPage(n))}
            onSize={(n) =>
              guard(() => {
                setPage(0);
                setSize(n);
              })
            }
          />
          <ErrorText error={rows.error} />
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
            className="h-full max-h-none rounded-none border-0"
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
            onFollow={(i, j) => onFollow(refs.get(cols[j].name)!, shown[i][j]!)}
          />
        )
      )}
    </BrowserShell>
  );
}
