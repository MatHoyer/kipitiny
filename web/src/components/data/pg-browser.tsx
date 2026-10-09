import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight, Download, Plus, RefreshCw, Table2, X } from "lucide-react";
import { useState } from "react";
import { Empty, ErrorText, Loading } from "@/components/common";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";
import { api, type PgFilter, type PgFilterOp, type PgTable } from "../../api";
import { DataTable } from "./data-table";

const PAGE = 50;

const ops: { op: PgFilterOp; label: string; unary?: boolean }[] = [
  { op: "=", label: "=" },
  { op: "!=", label: "≠" },
  { op: "<", label: "<" },
  { op: "<=", label: "≤" },
  { op: ">", label: ">" },
  { op: ">=", label: "≥" },
  { op: "like", label: "like" },
  { op: "null", label: "is null", unary: true },
  { op: "notnull", label: "is not null", unary: true },
];

const tableKey = (t: Pick<PgTable, "schema" | "name">) => `${t.schema}.${t.name}`;

export function PgBrowser({ serviceId }: { serviceId: string }) {
  const tables = useQuery({ queryKey: ["data", serviceId, "tables"], queryFn: () => api.pgTables(serviceId) });
  const [picked, setPicked] = useState<PgTable | null>(null);

  if (tables.isPending) return <Loading />;
  if (tables.error) return <ErrorText error={tables.error} />;
  const list = tables.data ?? [];
  if (list.length === 0) return <Empty>No tables yet.</Empty>;
  const current = list.find((t) => picked && tableKey(t) === tableKey(picked)) ?? list[0];
  const schemas = [...new Set(list.map((t) => t.schema))];

  return (
    <div className="grid gap-4 md:grid-cols-[14rem_minmax(0,1fr)]">
      <nav aria-label="Tables" className="max-h-[70vh] space-y-3 overflow-y-auto md:border-r md:pr-3">
        <div className="flex items-center justify-between">
          <span className="text-xs font-medium text-muted-foreground">{list.length} tables</span>
          <Button variant="ghost" size="icon-xs" title="Refresh" onClick={() => tables.refetch()}>
            <RefreshCw className={cn(tables.isFetching && "animate-spin")} />
          </Button>
        </div>
        {schemas.map((schema) => (
          <div key={schema} className="space-y-0.5">
            {schemas.length > 1 && <p className="px-2 text-xs font-medium text-muted-foreground">{schema}</p>}
            {list
              .filter((t) => t.schema === schema)
              .map((t) => (
                <button
                  key={tableKey(t)}
                  type="button"
                  onClick={() => setPicked(t)}
                  aria-current={tableKey(t) === tableKey(current)}
                  className="flex w-full items-center gap-2 rounded-md px-2 py-1 text-left text-sm hover:bg-muted aria-[current=true]:bg-muted aria-[current=true]:font-medium"
                >
                  <Table2 className="size-3.5 shrink-0 text-muted-foreground" />
                  <span className="min-w-0 flex-1 truncate">{t.name}</span>
                  <span className="text-xs text-muted-foreground">{t.kind === "table" ? rowCount(t.rowEstimate) : t.kind}</span>
                </button>
              ))}
          </div>
        ))}
      </nav>
      <TableView key={tableKey(current)} serviceId={serviceId} table={current} />
    </div>
  );
}

function rowCount(n: number) {
  if (n < 0) return "";
  return n >= 1000 ? `~${Intl.NumberFormat(undefined, { notation: "compact" }).format(n)}` : `~${n}`;
}

function TableView({ serviceId, table }: { serviceId: string; table: PgTable }) {
  const [offset, setOffset] = useState(0);
  const [sort, setSort] = useState<{ column: string; desc: boolean } | undefined>();
  const [filters, setFilters] = useState<PgFilter[]>([]);
  const q = { limit: PAGE, offset, orderBy: sort?.column, desc: sort?.desc, filters };
  const rows = useQuery({
    queryKey: ["data", serviceId, "rows", table.schema, table.name, q],
    queryFn: () => api.pgRows(serviceId, table.schema, table.name, q),
    placeholderData: keepPreviousData,
  });
  const data = rows.data;
  const toggleSort = (column: string) => {
    setOffset(0);
    setSort((s) => (s?.column !== column ? { column, desc: false } : s.desc ? undefined : { column, desc: true }));
  };

  return (
    <div className="min-w-0 space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h3 className="font-medium">
            {table.schema !== "public" && <span className="text-muted-foreground">{table.schema}.</span>}
            {table.name}
          </h3>
          <p className="text-xs text-muted-foreground">
            {table.kind}
            {table.bytes > 0 && ` · ${formatBytes(table.bytes)}`}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" asChild>
            <a href={api.pgExportUrl(serviceId, table.schema, table.name, q)} download>
              <Download /> CSV
            </a>
          </Button>
          <Button variant="ghost" size="icon-sm" title="Refresh" onClick={() => rows.refetch()}>
            <RefreshCw className={cn(rows.isFetching && "animate-spin")} />
          </Button>
        </div>
      </div>
      {data && (
        <Filters
          columns={data.columns.map((c) => c.name)}
          filters={filters}
          onChange={(f) => {
            setOffset(0);
            setFilters(f);
          }}
        />
      )}
      <ErrorText error={rows.error} />
      {rows.isPending ? (
        <Loading />
      ) : (
        data && (
          <>
            <DataTable
              columns={data.columns.map((c) => ({ name: c.name, hint: c.type, primaryKey: c.primaryKey }))}
              rows={data.rows}
              truncated={data.truncated}
              sort={sort}
              onSort={toggleSort}
              empty={filters.length ? "No rows match." : "No rows."}
            />
            <div className="flex items-center justify-end gap-2 text-xs text-muted-foreground">
              {data.rows.length > 0 && (
                <span>
                  {offset + 1}–{offset + data.rows.length}
                </span>
              )}
              <Button variant="outline" size="icon-xs" title="Previous page" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - PAGE))}>
                <ChevronLeft />
              </Button>
              <Button variant="outline" size="icon-xs" title="Next page" disabled={!data.hasMore} onClick={() => setOffset(offset + PAGE)}>
                <ChevronRight />
              </Button>
            </div>
          </>
        )
      )}
    </div>
  );
}

/** Filters apply on submit, not on every keystroke. */
function Filters({ columns, filters, onChange }: { columns: string[]; filters: PgFilter[]; onChange: (f: PgFilter[]) => void }) {
  const [draft, setDraft] = useState<PgFilter[]>(filters);
  const set = (i: number, f: Partial<PgFilter>) => setDraft(draft.map((d, j) => (j === i ? { ...d, ...f } : d)));
  const dirty = JSON.stringify(draft) !== JSON.stringify(filters);

  return (
    <form
      className="flex flex-wrap items-center gap-2"
      onSubmit={(e) => {
        e.preventDefault();
        onChange(draft);
      }}
    >
      {draft.map((f, i) => {
        const unary = ops.find((o) => o.op === f.op)?.unary;
        return (
          <div key={i} className="flex items-center gap-1 rounded-lg border p-1">
            <Select value={f.column} onValueChange={(column) => set(i, { column })}>
              <SelectTrigger size="sm" aria-label="Column" className="border-0">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {columns.map((c) => (
                  <SelectItem key={c} value={c}>
                    {c}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select value={f.op} onValueChange={(op) => set(i, { op: op as PgFilterOp })}>
              <SelectTrigger size="sm" aria-label="Operator" className="border-0">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {ops.map((o) => (
                  <SelectItem key={o.op} value={o.op}>
                    {o.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {!unary && (
              <Input
                aria-label="Value"
                className="h-8 w-36"
                value={f.value ?? ""}
                placeholder={f.op === "like" ? "%pattern%" : "value"}
                onChange={(e) => set(i, { value: e.target.value })}
              />
            )}
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              title="Remove filter"
              onClick={() => {
                const next = draft.filter((_, j) => j !== i);
                setDraft(next);
                onChange(next);
              }}
            >
              <X />
            </Button>
          </div>
        );
      })}
      <Button type="button" variant="ghost" size="sm" onClick={() => setDraft([...draft, { column: columns[0], op: "=", value: "" }])}>
        <Plus /> Filter
      </Button>
      {dirty && draft.length > 0 && (
        <Button type="submit" size="sm">
          Apply
        </Button>
      )}
    </form>
  );
}
