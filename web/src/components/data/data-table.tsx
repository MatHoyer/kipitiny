import { ArrowDown, ArrowUp, ArrowUpRight, KeyRound, Maximize2, Trash2, Undo2 } from "lucide-react";
import { useMemo, useRef, useState, type ReactNode } from "react";
import { CopyButton } from "@/components/common";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";
import { prettyValue } from "./values";

/** The server's cap on a cell (DataCellMax). */
export const CELL_MAX = 4096;

/** references names the row a value points to (a foreign key), followed with onFollow. */
export type DataColumn = { name: string; type?: string; primaryKey?: boolean; required?: boolean; references?: string };

/** A cell: text, null for NULL, undefined for a new row's DEFAULT. */
export type Cell = string | null | undefined;

/**
 * Editing a grid: changes are only staged, the caller keeps and saves them.
 * Rows are as they would be once saved; state and was say what changed.
 */
export type GridEdit = {
  /** A row added, or marked for deletion. */
  state: (row: number) => "new" | "deleted" | undefined;
  /** A changed cell's value as loaded; undefined when unchanged. */
  was: (row: number, col: number) => { value: Cell } | undefined;
  /** Why a cell can't be edited; undefined when it can. Column -1 is the row itself: why it can't be deleted. */
  locked: (row: number, col: number) => string | undefined;
  onEdit: (row: number, col: number, value: Cell) => void;
  /** Marks a row for deletion or takes the mark back; drops a new row. */
  onDelete: (row: number) => void;
  /** Cells can hold NULL (SQL): the row's dialog offers it. */
  nulls?: boolean;
};

const display = (v: Cell) => (v === undefined ? "DEFAULT" : v === null ? "NULL" : v);

/** Opens the row a foreign key value points to. */
function FollowButton({ column, onFollow }: { column: DataColumn; onFollow: () => void }) {
  return (
    <button
      type="button"
      title={`Open in ${column.references}`}
      aria-label={`Open in ${column.references}`}
      onClick={(e) => {
        e.stopPropagation();
        onFollow();
      }}
      className="mr-1 inline-flex rounded align-[-2px] text-muted-foreground hover:bg-muted hover:text-foreground"
    >
      <ArrowUpRight className="size-3.5" />
    </button>
  );
}

/**
 * A spreadsheet-like grid of text cells (null is NULL). Clicking a row opens
 * it whole in a dialog; with edit, clicking a cell edits it in place and the
 * dialog edits the whole row. Sorting is the caller's.
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
  edit,
  onFollow,
}: {
  columns: DataColumn[];
  rows: Cell[][];
  truncated?: [number, number][];
  sort?: { column: string; desc: boolean };
  onSort?: (column: string) => void;
  /** Names a row in its sheet; defaults to its position. */
  rowLabel?: (row: Cell[], i: number) => string;
  empty?: ReactNode;
  className?: string;
  edit?: GridEdit;
  /** Follows the foreign key of a cell whose column has references. */
  onFollow?: (row: number, col: number) => void;
}) {
  const [open, setOpen] = useState<number | null>(null);
  const [editing, setEditing] = useState<[number, number] | null>(null);
  const cut = useMemo(() => new Set(truncated.map(([r, c]) => `${r}:${c}`)), [truncated]);
  const row = open === null ? undefined : rows[open];
  const follows = (i: number, j: number) => !!onFollow && !!columns[j].references && rows[i][j] != null && !cut.has(`${i}:${j}`) && !edit?.state(i) && !edit?.was(i, j);
  const canEdit = (i: number, j: number) => !!edit && edit.state(i) !== "deleted" && !edit.locked(i, j);
  const nextCell = (i: number, j: number, step: number) => {
    for (let k = j + step; k >= 0 && k < columns.length; k += step) if (canEdit(i, k)) return k;
    return null;
  };

  return (
    <>
      <div className={cn("max-h-[65vh] overflow-auto rounded-lg border bg-card", className)}>
        <table className="w-max min-w-full border-separate border-spacing-0 font-mono text-xs">
          <thead className="sticky top-0 z-10 bg-card">
            <tr>
              {edit && <th scope="col" className="sticky left-0 z-[1] w-px border-r border-b bg-card" aria-label="Row actions" />}
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
            {rows.map((r, i) => {
              const state = edit?.state(i);
              const label = rowLabel ? rowLabel(r, i) : `row ${i + 1}`;
              return (
                <tr
                  key={i}
                  tabIndex={0}
                  aria-label={`${edit ? "" : "Open "}${label}${state === "new" ? ", new" : state === "deleted" ? ", to delete" : ""}`}
                  onClick={edit ? undefined : () => setOpen(i)}
                  onKeyDown={(e) => e.key === "Enter" && e.target === e.currentTarget && setOpen(i)}
                  className={cn(
                    "outline-none even:bg-muted/40 focus-visible:bg-muted",
                    !edit && "cursor-pointer hover:bg-muted",
                  )}
                >
                  {edit && (
                    <td
                      className={cn(
                        "sticky left-0 z-[1] border-r border-b bg-card px-1 py-0.5 whitespace-nowrap",
                        state === "new" && "shadow-[inset_2px_0_0] shadow-emerald-500",
                        state === "deleted" && "shadow-[inset_2px_0_0] shadow-destructive",
                      )}
                    >
                      <span className="flex items-center">
                        <Button variant="ghost" size="icon-xs" title={`Open ${label}`} onClick={() => setOpen(i)}>
                          <Maximize2 />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon-xs"
                          title={state === "deleted" ? "Keep this row" : state === "new" ? "Remove this new row" : "Delete this row"}
                          disabled={state !== "new" && state !== "deleted" && !!edit.locked(i, -1)}
                          onClick={() => edit.onDelete(i)}
                          className={cn(state !== "deleted" && "hover:text-destructive")}
                        >
                          {state === "deleted" ? <Undo2 /> : <Trash2 />}
                        </Button>
                      </span>
                    </td>
                  )}
                  {r.map((v, j) => {
                    const was = edit?.was(i, j);
                    const locked = edit?.locked(i, j);
                    if (editing?.[0] === i && editing[1] === j)
                      return (
                        <td key={j} className="max-w-72 border-r border-b p-0 outline-2 -outline-offset-2 outline-ring last:border-r-0">
                          <CellInput
                            value={v}
                            onDone={(value, move) => {
                              if (value !== undefined) edit!.onEdit(i, j, value);
                              const k = move ? nextCell(i, j, move) : null;
                              setEditing(k === null ? null : [i, k]);
                            }}
                          />
                        </td>
                      );
                    return (
                      <td
                        key={j}
                        title={was ? `Was: ${display(was.value)}` : edit && locked && state !== "deleted" ? locked : undefined}
                        onClick={edit && canEdit(i, j) ? () => setEditing([i, j]) : undefined}
                        className={cn(
                          "max-w-72 truncate border-r border-b px-2.5 py-2 last:border-r-0",
                          edit && (canEdit(i, j) ? "cursor-text hover:bg-muted" : state !== "deleted" && "cursor-not-allowed"),
                          state === "new" && "bg-emerald-500/10",
                          state === "deleted" && "bg-destructive/5 text-muted-foreground line-through",
                          was && state !== "deleted" && "relative bg-amber-500/15",
                        )}
                      >
                        {v === null || v === undefined ? (
                          <span className="text-muted-foreground/70">{display(v)}</span>
                        ) : (
                          <>
                            {follows(i, j) && <FollowButton column={columns[j]} onFollow={() => onFollow!(i, j)} />}
                            {v.replace(/\s+/g, " ")}
                            {cut.has(`${i}:${j}`) && !was && "…"}
                          </>
                        )}
                        {was && state !== "deleted" && <span className="absolute top-0.5 right-0.5 size-1.5 rounded-full bg-amber-500" />}
                      </td>
                    );
                  })}
                </tr>
              );
            })}
          </tbody>
        </table>
        {rows.length === 0 && <p className="px-3 py-10 text-center font-sans text-sm text-muted-foreground">{empty}</p>}
      </div>
      <Dialog open={!!row} onOpenChange={(o) => !o && setOpen(null)}>
        {/* No focus on open: it would land on a copy button and pop its tooltip. */}
        <DialogContent className="flex max-h-[85vh] flex-col gap-0 p-0 sm:max-w-2xl" onOpenAutoFocus={(e) => e.preventDefault()}>
          {row && open !== null && (
            <RowDetail
              columns={columns}
              row={row}
              title={rowLabel ? rowLabel(row, open) : `Row ${open + 1}`}
              isCut={(j) => cut.has(`${open}:${j}`)}
              follow={(j) =>
                follows(open, j)
                  ? () => {
                      setOpen(null);
                      onFollow!(open, j);
                    }
                  : undefined
              }
              edit={
                edit && {
                  nulls: edit.nulls ?? false,
                  state: edit.state(open),
                  was: (j) => edit.was(open, j),
                  locked: (j) => edit.locked(open, j),
                  onEdit: (j, v) => edit.onEdit(open, j, v),
                  onDelete: () => {
                    // A new row is gone once removed.
                    if (edit.state(open) === "new") setOpen(null);
                    edit.onDelete(open);
                  },
                }
              }
            />
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}

/**
 * A cell's text being edited. Enter or leaving it keeps the text, Escape
 * drops it, Tab moves to the next cell. Empty text on a NULL or DEFAULT cell
 * leaves it as is: the row's dialog sets NULL.
 */
function CellInput({ value, onDone }: { value: Cell; onDone: (value: Cell, move?: number) => void }) {
  const done = useRef(false);
  const finish = (text: string | null, move?: number) => {
    if (done.current) return;
    done.current = true;
    const unchanged = text === null || text === (value ?? "") || (text === "" && (value === null || value === undefined));
    onDone(unchanged ? undefined : text, move);
  };
  return (
    <input
      autoFocus
      defaultValue={value ?? ""}
      placeholder={display(value)}
      aria-label="Cell value"
      spellCheck={false}
      onFocus={(e) => e.target.select()}
      onBlur={(e) => finish(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === "Enter") finish(e.currentTarget.value);
        else if (e.key === "Escape") finish(null);
        else if (e.key === "Tab") {
          e.preventDefault();
          finish(e.currentTarget.value, e.shiftKey ? -1 : 1);
        }
      }}
      className="w-full min-w-32 bg-background px-2.5 py-2 outline-none placeholder:text-muted-foreground/70"
    />
  );
}

type RowEdit = {
  nulls: boolean;
  state: "new" | "deleted" | undefined;
  was: (col: number) => { value: Cell } | undefined;
  locked: (col: number) => string | undefined;
  onEdit: (col: number, value: Cell) => void;
  onDelete: () => void;
};

/** A row whole: each field with its type, editable when edit is given. */
function RowDetail({
  columns,
  row,
  title,
  isCut,
  follow,
  edit,
}: {
  columns: DataColumn[];
  row: Cell[];
  title: string;
  isCut: (j: number) => boolean;
  /** Opens the row a field's foreign key points to; undefined when it has none. */
  follow: (j: number) => (() => void) | undefined;
  edit?: RowEdit;
}) {
  const editable = edit && edit.state !== "deleted";
  return (
    <>
      <DialogHeader className="border-b p-4">
        <DialogTitle className="font-mono text-sm">
          {title}
          {edit?.state === "new" && <span className="ml-2 font-sans text-xs font-normal text-emerald-600 dark:text-emerald-400">new</span>}
          {edit?.state === "deleted" && <span className="ml-2 font-sans text-xs font-normal text-destructive">to delete</span>}
        </DialogTitle>
        <DialogDescription>{edit ? (editable ? "Changes are kept when you leave a field, and saved with the table's Save." : "Saving deletes this row.") : `${columns.length} fields`}</DialogDescription>
      </DialogHeader>
      <dl className="min-h-0 flex-1 divide-y overflow-y-auto">
        {columns.map((c, j) => {
          const v = row[j];
          const cut = isCut(j);
          const was = edit?.was(j);
          const locked = edit?.locked(j);
          const onFollow = follow(j);
          return (
            <div key={c.name} className="group/field grid gap-1 px-4 py-2.5 sm:grid-cols-[11rem_minmax(0,1fr)] sm:gap-4">
              <dt className="flex items-start gap-1.5 pt-px font-mono text-xs">
                {c.primaryKey && <KeyRound className="mt-px size-3 shrink-0 text-amber-500" />}
                <span className="min-w-0">
                  <span className="font-medium break-all">{c.name}</span>
                  {c.type && <span className="block text-muted-foreground">{c.type}</span>}
                  {was && <span className="mt-0.5 block font-sans text-amber-600 dark:text-amber-400">edited</span>}
                </span>
              </dt>
              <dd className="flex min-w-0 items-start gap-2 font-mono text-xs">
                {editable && !locked ? (
                  <FieldInput value={v} isNew={edit.state === "new"} nullable={edit.nulls && !c.required && !c.primaryKey} onEdit={(value) => edit.onEdit(j, value)} />
                ) : (
                  <span className="min-w-0 flex-1 break-all whitespace-pre-wrap">
                    {v === null || v === undefined ? <span className="text-muted-foreground/70">{display(v)}</span> : prettyValue(v, cut)}
                    {cut && (
                      <span className="mt-1 block font-sans text-muted-foreground">
                        First {formatBytes(CELL_MAX)} shown. Export the table as CSV for the whole value.
                      </span>
                    )}
                    {editable && locked && <span className="mt-1 block font-sans text-muted-foreground">{locked}</span>}
                  </span>
                )}
                {onFollow && (
                  <Button variant="ghost" size="icon-xs" title={`Open in ${c.references}`} onClick={onFollow}>
                    <ArrowUpRight />
                  </Button>
                )}
                {v !== null && v !== undefined && (
                  <span className="opacity-0 group-hover/field:opacity-100 focus-within:opacity-100">
                    <CopyButton value={v} />
                  </span>
                )}
              </dd>
              {was && <dd className="font-mono text-xs text-muted-foreground sm:col-start-2">was {display(was.value)}</dd>}
            </div>
          );
        })}
      </dl>
      <div className="flex items-center justify-end gap-1 border-t px-4 py-2.5">
        {edit && (
          <Button variant="ghost" size="sm" className={cn("mr-auto", edit.state !== "deleted" && "text-destructive")} disabled={!edit.state && !!edit.locked(-1)} onClick={edit.onDelete}>
            {edit.state === "deleted" ? (
              <>
                <Undo2 /> Keep this row
              </>
            ) : (
              <>
                <Trash2 /> {edit.state === "new" ? "Remove this new row" : "Delete this row"}
              </>
            )}
          </Button>
        )}
        <span className="text-xs text-muted-foreground">Copy row as JSON</span>
        <CopyButton label="Copy row as JSON" value={JSON.stringify(Object.fromEntries(columns.map((c, j) => [c.name, row[j] ?? null])), null, 2)} />
      </div>
    </>
  );
}

/** A field of the row dialog: its text, plus NULL when allowed and DEFAULT on a new row. */
function FieldInput({ value, isNew, nullable, onEdit }: { value: Cell; isNew: boolean; nullable: boolean; onEdit: (v: Cell) => void }) {
  const special = (v: Cell, label: string) => (
    <button
      type="button"
      aria-pressed={value === v}
      onClick={() => value !== v && onEdit(v)}
      className="rounded border px-1.5 py-0.5 font-sans text-[0.7rem] text-muted-foreground hover:text-foreground aria-pressed:border-foreground/30 aria-pressed:bg-muted aria-pressed:text-foreground"
    >
      {label}
    </button>
  );
  return (
    <span className="flex min-w-0 flex-1 flex-col gap-1.5">
      <textarea
        // Remounted when the value changes elsewhere (NULL, DEFAULT).
        key={String(value)}
        defaultValue={value ?? ""}
        placeholder={display(value)}
        spellCheck={false}
        rows={1}
        onBlur={(e) => {
          const text = e.target.value;
          if (text !== (value ?? "")) onEdit(text);
        }}
        className="field-sizing-content max-h-64 min-h-8 w-full resize-none rounded-md border bg-background px-2 py-1.5 outline-none placeholder:text-muted-foreground/70 focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
      />
      {(nullable || isNew) && (
        <span className="flex gap-1">
          {nullable && special(null, "NULL")}
          {isNew && special(undefined, "DEFAULT")}
        </span>
      )}
    </span>
  );
}
