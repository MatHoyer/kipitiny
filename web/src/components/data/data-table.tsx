import { ArrowDown, ArrowUp, KeyRound } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";
import { CopyButton } from "@/components/common";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";
import { prettyValue } from "./values";

/** The server's cap on a cell (DataCellMax). */
export const CELL_MAX = 4096;

export type DataColumn = { name: string; type?: string; primaryKey?: boolean; required?: boolean };

/**
 * A spreadsheet-like, read-only grid of text cells (null is NULL). Clicking
 * a row opens it whole in a dialog. Sorting is the caller's.
 */
export function DataTable({
  columns,
  rows,
  truncated = [],
  sort,
  onSort,
  rowLabel,
  empty = "No rows.",
  className,
}: {
  columns: DataColumn[];
  rows: (string | null)[][];
  truncated?: [number, number][];
  sort?: { column: string; desc: boolean };
  onSort?: (column: string) => void;
  /** Names a row in its sheet; defaults to its position. */
  rowLabel?: (row: (string | null)[], i: number) => string;
  empty?: ReactNode;
  className?: string;
}) {
  const [open, setOpen] = useState<number | null>(null);
  const cut = useMemo(() => new Set(truncated.map(([r, c]) => `${r}:${c}`)), [truncated]);
  const row = open === null ? undefined : rows[open];

  return (
    <>
      <div className={cn("max-h-[65vh] overflow-auto rounded-lg border bg-card", className)}>
        <table className="w-max min-w-full border-separate border-spacing-0 font-mono text-xs">
          <thead className="sticky top-0 z-10 bg-card">
            <tr>
              {columns.map((c) => {
                const sorted = sort?.column === c.name;
                return (
                  <th
                    key={c.name}
                    scope="col"
                    aria-sort={sorted ? (sort.desc ? "descending" : "ascending") : undefined}
                    className="border-r border-b p-0 text-left font-normal whitespace-nowrap last:border-r-0"
                  >
                    <button
                      type="button"
                      disabled={!onSort}
                      onClick={() => onSort?.(c.name)}
                      className="flex w-full min-w-32 items-center gap-1.5 px-2.5 py-2 text-left enabled:hover:bg-muted/60 disabled:cursor-default"
                    >
                      {c.primaryKey && <KeyRound aria-label="primary key" className="size-3 shrink-0 text-amber-500" />}
                      {c.required && (
                        <span className="text-destructive" aria-label="required">
                          *
                        </span>
                      )}
                      <span className="font-medium text-foreground">{c.name}</span>
                      {c.type && <span className="text-muted-foreground">{c.type}</span>}
                      {sorted && (sort.desc ? <ArrowDown className="ml-auto size-3" /> : <ArrowUp className="ml-auto size-3" />)}
                    </button>
                  </th>
                );
              })}
            </tr>
          </thead>
          <tbody>
            {rows.map((r, i) => (
              <tr
                key={i}
                tabIndex={0}
                aria-label={`Open ${rowLabel ? rowLabel(r, i) : `row ${i + 1}`}`}
                onClick={() => setOpen(i)}
                onKeyDown={(e) => e.key === "Enter" && setOpen(i)}
                className="cursor-pointer outline-none even:bg-muted/40 hover:bg-muted focus-visible:bg-muted"
              >
                {r.map((v, j) => (
                  <td key={j} className="max-w-72 truncate border-r border-b px-2.5 py-2 last:border-r-0">
                    {v === null ? (
                      <span className="text-muted-foreground/70">NULL</span>
                    ) : (
                      <>
                        {v.replace(/\s+/g, " ")}
                        {cut.has(`${i}:${j}`) && "…"}
                      </>
                    )}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
        {rows.length === 0 && <p className="px-3 py-10 text-center font-sans text-sm text-muted-foreground">{empty}</p>}
      </div>
      <Dialog open={!!row} onOpenChange={(o) => !o && setOpen(null)}>
        {/* No focus on open: it would land on a copy button and pop its tooltip. */}
        <DialogContent className="flex max-h-[85vh] flex-col gap-0 p-0 sm:max-w-2xl" onOpenAutoFocus={(e) => e.preventDefault()}>
          {row && open !== null && (
            <>
              <DialogHeader className="border-b p-4">
                <DialogTitle className="font-mono text-sm">{rowLabel ? rowLabel(row, open) : `Row ${open + 1}`}</DialogTitle>
                <DialogDescription>{columns.length} fields</DialogDescription>
              </DialogHeader>
              <dl className="min-h-0 flex-1 divide-y overflow-y-auto">
                {columns.map((c, j) => {
                  const v = row[j];
                  const isCut = cut.has(`${open}:${j}`);
                  return (
                    <div key={c.name} className="group/field grid gap-1 px-4 py-2.5 sm:grid-cols-[11rem_minmax(0,1fr)] sm:gap-4">
                      <dt className="flex items-start gap-1.5 pt-px font-mono text-xs">
                        {c.primaryKey && <KeyRound className="mt-px size-3 shrink-0 text-amber-500" />}
                        <span className="min-w-0">
                          <span className="font-medium break-all">{c.name}</span>
                          {c.type && <span className="block text-muted-foreground">{c.type}</span>}
                        </span>
                      </dt>
                      <dd className="flex min-w-0 items-start gap-2 font-mono text-xs">
                        <span className="min-w-0 flex-1 break-all whitespace-pre-wrap">
                          {v === null ? <span className="text-muted-foreground/70">NULL</span> : prettyValue(v, isCut)}
                          {isCut && (
                            <span className="mt-1 block font-sans text-muted-foreground">
                              First {formatBytes(CELL_MAX)} shown. Export the table as CSV for the whole value.
                            </span>
                          )}
                        </span>
                        {v !== null && (
                          <span className="opacity-0 group-hover/field:opacity-100 focus-within:opacity-100">
                            <CopyButton value={v} />
                          </span>
                        )}
                      </dd>
                    </div>
                  );
                })}
              </dl>
              <div className="flex items-center justify-end gap-1 border-t px-4 py-2.5">
                <span className="text-xs text-muted-foreground">Copy row as JSON</span>
                <CopyButton label="Copy row as JSON" value={JSON.stringify(Object.fromEntries(columns.map((c, j) => [c.name, row[j]])), null, 2)} />
              </div>
            </>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}
