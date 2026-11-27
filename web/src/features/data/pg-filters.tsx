import { Check, ListFilter, X } from "lucide-react";
import { useRef, useState } from "react";
import { Button } from "@kipit-labs/kipitidy/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@kipit-labs/kipitidy/dropdown-menu";
import { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue } from "@kipit-labs/kipitidy/select";
import { cn } from "@/lib/utils";
import type { PgColumn, PgFilter, PgFilterOp } from "../../api";

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

type Draft = PgFilter & { id: number; applied: boolean };

const isUnary = (op: PgFilterOp) => op === "null" || op === "notnull";
const ready = (f: PgFilter) => isUnary(f.op) || (f.value ?? "") !== "";
const strip = ({ column, op, value }: PgFilter): PgFilter => (isUnary(op) ? { column, op } : { column, op, value });

/**
 * Filters as pills: pick a column from the menu, then an operator and a
 * value; ✓ (or Enter) applies it. Only applied, complete filters reach the
 * server, so removing an unfinished one changes nothing.
 */
export function useFilters(columns: PgColumn[], initial: PgFilter[], onChange: (f: PgFilter[]) => void) {
  const seq = useRef(initial.length);
  const focusNext = useRef<number | null>(null);
  const [draft, setDraft] = useState<Draft[]>(() => initial.map((f, i) => ({ ...f, id: i + 1, applied: true })));
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
