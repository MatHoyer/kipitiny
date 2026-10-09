import { ArrowDown, ArrowUp, KeyRound } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";
import { CopyButton } from "@/components/common";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { formatBytes } from "@/lib/format";

/** Cells longer than this are cut in the grid; the dialog shows what the server sent. */
const CELL_PREVIEW = 120;

export type DataColumn = { name: string; hint?: string; primaryKey?: boolean };

/** The server's cap on a cell (DataCellMax). */
export const CELL_MAX = 4096;

/**
 * A read-only grid of text cells (null is NULL). Clicking a cell opens it in
 * full; cells cut by the server are flagged. Sorting is the caller's.
 */
export function DataTable({
  columns,
  rows,
  truncated = [],
  sort,
  onSort,
  empty = "No rows.",
}: {
  columns: DataColumn[];
  rows: (string | null)[][];
  truncated?: [number, number][];
  sort?: { column: string; desc: boolean };
  onSort?: (column: string) => void;
  empty?: ReactNode;
}) {
  const [open, setOpen] = useState<{ column: string; value: string; cut: boolean } | null>(null);
  const cut = useMemo(() => new Set(truncated.map(([r, c]) => `${r}:${c}`)), [truncated]);

  return (
    <>
      <div className="max-h-[65vh] overflow-auto rounded-lg border">
        <table className="w-full border-collapse text-sm">
          <thead className="sticky top-0 z-10 bg-muted">
            <tr>
              {columns.map((c) => (
                <th key={c.name} scope="col" className="border-b px-3 py-2 text-left font-medium whitespace-nowrap">
                  <button
                    type="button"
                    disabled={!onSort}
                    onClick={() => onSort?.(c.name)}
                    className="inline-flex items-center gap-1 enabled:hover:text-foreground disabled:cursor-default"
                  >
                    {c.primaryKey && <KeyRound className="size-3 text-muted-foreground" aria-label="primary key" />}
                    {c.name}
                    {c.hint && <span className="font-normal text-muted-foreground">{c.hint}</span>}
                    {sort?.column === c.name && (sort.desc ? <ArrowDown className="size-3" /> : <ArrowUp className="size-3" />)}
                  </button>
                </th>
              ))}
            </tr>
          </thead>
          <tbody className="font-mono text-xs">
            {rows.map((row, i) => (
              <tr key={i} className="border-b last:border-0 hover:bg-muted/40">
                {row.map((v, j) => {
                  const isCut = cut.has(`${i}:${j}`);
                  return (
                    <td key={j} className="max-w-80 px-3 py-1.5 align-top">
                      {v === null ? (
                        <span className="text-muted-foreground italic">NULL</span>
                      ) : (
                        <button
                          type="button"
                          className="block max-w-full truncate text-left hover:underline"
                          onClick={() => setOpen({ column: columns[j]?.name ?? "", value: v, cut: isCut })}
                        >
                          {v === "" ? <span className="text-muted-foreground italic">empty</span> : preview(v)}
                          {isCut && <span className="text-muted-foreground">…</span>}
                        </button>
                      )}
                    </td>
                  );
                })}
              </tr>
            ))}
          </tbody>
        </table>
        {rows.length === 0 && <p className="px-3 py-6 text-center text-sm text-muted-foreground">{empty}</p>}
      </div>
      <Dialog open={!!open} onOpenChange={(o) => !o && setOpen(null)}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              {open?.column}
              {open && <CopyButton value={open.value} />}
            </DialogTitle>
            <DialogDescription>
              {open && formatBytes(new TextEncoder().encode(open.value).length)}
              {open?.cut && ` shown: the value is longer than ${formatBytes(CELL_MAX)} and was cut.`}
            </DialogDescription>
          </DialogHeader>
          <pre className="max-h-[60vh] overflow-auto rounded-md bg-muted p-3 font-mono text-xs break-all whitespace-pre-wrap">
            {open && pretty(open.value, open.cut)}
          </pre>
        </DialogContent>
      </Dialog>
    </>
  );
}

function preview(v: string) {
  const line = v.replace(/\s+/g, " ");
  return line.length > CELL_PREVIEW ? line.slice(0, CELL_PREVIEW) + "…" : line;
}

/** Indents JSON documents; anything else (or a cut document) as is. */
function pretty(v: string, cut: boolean) {
  if (cut || !/^\s*[[{]/.test(v)) return v;
  try {
    return JSON.stringify(JSON.parse(v), null, 2);
  } catch {
    return v;
  }
}

