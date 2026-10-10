import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { Check, ChevronLeft, ChevronRight, ChevronsLeft, Download, ListFilter, RefreshCw, X } from "lucide-react";
import { useRef, useState, type ReactNode } from "react";
import { Empty, ErrorText, Loading } from "@/components/common";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue } from "@/components/ui/select";
import { cn } from "@/lib/utils";
import { api, type PgColumn, type PgFilter, type PgFilterOp, type PgTable } from "../../api";
import { BrowserShell, SearchBox, SidebarHeader, SidebarItem, SidebarToggle } from "./browser-shell";
import { DataTable } from "./data-table";

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

const tableKey = (t: Pick<PgTable, "schema" | "name">) => `${t.schema}.${t.name}`;

/** defaultSchema's tables are listed without a heading: public, or MySQL's database. */
export function PgBrowser({ serviceId, database, defaultSchema }: { serviceId: string; database: string; defaultSchema: string }) {
  const tables = useQuery({ queryKey: ["data", serviceId, "tables", database], queryFn: () => api.pgTables(serviceId, database) });
  const [picked, setPicked] = useState<string | null>(null);
  const [find, setFind] = useState("");
  const [sidebar, setSidebar] = useState(true);

  if (tables.isPending) return <Loading />;
  if (tables.error) return <ErrorText error={tables.error} />;
  const list = tables.data ?? [];
  if (list.length === 0) return <Empty>No tables in this database yet. Once your app runs its migrations, its tables show up here.</Empty>;
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
          <Button variant="ghost" size="icon-xs" title="Reload tables" onClick={() => tables.refetch()}>
            <RefreshCw className={cn(tables.isFetching && "animate-spin")} />
          </Button>
        }
      />
      <nav aria-label="Tables" className="flex-1 space-y-3 overflow-y-auto p-2">
        {shown.length === 0 && <p className="px-2 text-xs text-muted-foreground">No table matches.</p>}
        {schemas.map((schema) => (
          <div key={schema} className="space-y-px">
            {(schemas.length > 1 || schema !== defaultSchema) && <p className="px-2 pb-1 text-xs text-muted-foreground">{schema}</p>}
            {shown
              .filter((t) => t.schema === schema)
              .map((t) => (
                <SidebarItem key={tableKey(t)} selected={tableKey(t) === tableKey(current)} onClick={() => setPicked(tableKey(t))} title={t.kind}>
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
      onPick={setPicked}
      sidebar={nav}
      sidebarOpen={sidebar}
      onToggleSidebar={() => setSidebar(!sidebar)}
    />
  );
}

function TableView({
  serviceId,
  database,
  table,
  tables,
  onPick,
  sidebar,
  sidebarOpen,
  onToggleSidebar,
}: {
  serviceId: string;
  database: string;
  table: PgTable;
  tables: PgTable[];
  onPick: (key: string) => void;
  sidebar: ReactNode;
  sidebarOpen: boolean;
  onToggleSidebar: () => void;
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
  const filterBar = useFilters(cols, (f) => {
    setPage(0);
    setFilters(f);
  });

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
            onChange={(s) => {
              setPage(0);
              setSearch(s);
            }}
            placeholder="Search rows"
            className="w-44 sm:w-56"
          />
          {filterBar.trigger}
          <Button variant="outline" size="icon-sm" asChild>
            <a href={api.pgExportUrl(serviceId, database, table.schema, table.name, q)} download title="Export the rows matching the search and filters as CSV" aria-label="Export CSV">
              <Download />
            </a>
          </Button>
          <Button variant="outline" size="icon-sm" className="ml-auto" title="Reload rows" onClick={() => rows.refetch()}>
            <RefreshCw className={cn(rows.isFetching && "animate-spin")} />
          </Button>
          {filterBar.pills}
        </>
      }
      footer={
        <>
          <div className="flex h-8 items-stretch overflow-hidden rounded-md border">
            <Button variant="ghost" size="icon-sm" className="h-auto rounded-none" title="First page" disabled={page === 0} onClick={() => setPage(0)}>
              <ChevronsLeft />
            </Button>
            <Button variant="ghost" size="icon-sm" className="h-auto rounded-none border-l" title="Previous page" disabled={page === 0} onClick={() => setPage(page - 1)}>
              <ChevronLeft />
            </Button>
            <span className="flex items-center border-l px-3 text-sm whitespace-nowrap tabular-nums">
              {page + 1}
              {pages && <span className="ml-1 text-muted-foreground">of ~{Intl.NumberFormat().format(Math.max(pages, page + 1))}</span>}
            </span>
            <Select
              value={String(size)}
              onValueChange={(v) => {
                setPage(0);
                setSize(Number(v));
              }}
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
            <Button variant="ghost" size="icon-sm" className="h-auto rounded-none border-l" title="Next page" disabled={!data?.hasMore} onClick={() => setPage(page + 1)}>
              <ChevronRight />
            </Button>
          </div>
          <ErrorText error={rows.error} />
        </>
      }
    >
      {rows.isPending ? (
        <Loading />
      ) : (
        data && (
          <DataTable
            className="max-h-none rounded-none border-0"
            columns={columns}
            rows={data.rows}
            truncated={data.truncated}
            sort={sort}
            onSort={(column) => {
              setPage(0);
              setSort((s) => (s?.column !== column ? { column, desc: false } : s.desc ? undefined : { column, desc: true }));
            }}
            rowLabel={(r, i) => (pk.length ? pk.map((j) => `${cols[j].name} ${r[j]}`).join(", ") : `Row ${page * size + i + 1}`)}
            empty={filters.length || search ? "No row matches." : "This table is empty."}
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
