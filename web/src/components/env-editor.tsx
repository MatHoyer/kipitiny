import { Braces, Code, Link2, List, Lock, Plus, Trash2, X } from "lucide-react";
import { useState, type ReactNode } from "react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { envRef, formatEnvRows, parseEnvRows, refsIn, soleRef, type EnvRow } from "@/lib/format";
import { cn } from "@/lib/utils";
import { SECRET_MASK } from "../api";

const keyRe = /^[A-Za-z_][A-Za-z0-9_]*$/;

type Mode = "list" | "raw";

/**
 * Edits environment variables as a list of fields or as KEY=value text. With
 * vars (the project's shared variable names), values can reference them as
 * {{ project.NAME }}, picked from a menu instead of typed.
 */
export function EnvEditor({
  label = "Environment",
  description,
  rows,
  onChange,
  vars,
  className,
}: {
  label?: string;
  description?: ReactNode;
  rows: EnvRow[];
  onChange: (rows: EnvRow[]) => void;
  vars?: string[];
  className?: string;
}) {
  const [mode, setMode] = useState<Mode>("list");
  const [text, setText] = useState("");
  const [focus, setFocus] = useState(-1);

  const switchMode = (m: Mode) => {
    if (!m || m === mode) return;
    if (m === "raw") setText(formatEnvRows(rows));
    setMode(m);
  };
  const update = (i: number, row: Partial<EnvRow>) => onChange(rows.map((r, j) => (j === i ? { ...r, ...row } : r)));
  const add = (row: EnvRow) => {
    setFocus(rows.length);
    onChange([...rows, row]);
  };
  const counts = new Map<string, number>();
  for (const r of rows) counts.set(r.key, (counts.get(r.key) ?? 0) + 1);
  const unused = vars?.filter((v) => !rows.some((r) => r.key === v)) ?? [];

  return (
    <div className={cn("space-y-2", className)}>
      <div className="flex items-center justify-between gap-2">
        <span className="text-sm font-medium">{label}</span>
        <ToggleGroup
          type="single"
          variant="outline"
          size="sm"
          spacing={0}
          value={mode}
          onValueChange={(v) => switchMode(v as Mode)}
          aria-label="Editor view"
        >
          <ToggleGroupItem value="list" className="aria-checked:bg-muted">
            <List />
            Fields
          </ToggleGroupItem>
          <ToggleGroupItem value="raw" className="aria-checked:bg-muted">
            <Code />
            Text
          </ToggleGroupItem>
        </ToggleGroup>
      </div>

      {mode === "raw" ? (
        <Textarea
          aria-label={label}
          rows={Math.max(4, rows.length + 1)}
          value={text}
          onChange={(e) => {
            setText(e.target.value);
            onChange(parseEnvRows(e.target.value));
          }}
          placeholder={vars ? `NODE_ENV=production\nAPI_URL=${envRef("API_URL")}` : "NODE_ENV=production"}
          spellCheck={false}
          className="font-mono text-sm"
        />
      ) : (
        <div className="space-y-2">
          {rows.length === 0 && <p className="text-sm text-muted-foreground">No variables.</p>}
          {rows.map((row, i) => {
            const badKey = !!row.key && (!keyRe.test(row.key) || (counts.get(row.key) ?? 0) > 1);
            const unknown = vars ? refsIn(row.value).filter((r) => !vars.includes(r)) : [];
            return (
              <div key={i} className="space-y-1">
                <div className="flex items-start gap-2">
                  <Input
                    aria-label="Name"
                    placeholder="NAME"
                    value={row.key}
                    autoFocus={i === focus}
                    aria-invalid={badKey || undefined}
                    onChange={(e) => update(i, { key: e.target.value })}
                    spellCheck={false}
                    className="w-2/5 shrink-0 font-mono text-sm"
                  />
                  <ValueField
                    value={row.value}
                    onChange={(value) => update(i, { value })}
                    invalid={unknown.length > 0}
                    refs={!!vars}
                  />
                  {vars && vars.length > 0 && (
                    <VarMenu
                      vars={vars}
                      label="Reference a project variable"
                      onPick={(v) => update(i, { key: row.key || v, value: envRef(v) })}
                      trigger={
                        <Button type="button" variant="ghost" size="icon" aria-label="Reference a project variable">
                          <Braces />
                        </Button>
                      }
                    />
                  )}
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    aria-label={`Remove ${row.key || "variable"}`}
                    onClick={() => onChange(rows.filter((_, j) => j !== i))}
                  >
                    <Trash2 />
                  </Button>
                </div>
                {(badKey || unknown.length > 0) && (
                  <p className="px-1 text-xs text-destructive">
                    {badKey
                      ? (counts.get(row.key) ?? 0) > 1
                        ? "Duplicate name."
                        : "Letters, digits and _; not starting with a digit."
                      : `Unknown project variable ${unknown.join(", ")}.`}
                  </p>
                )}
              </div>
            );
          })}
          <div className="flex flex-wrap gap-2">
            <Button type="button" variant="outline" size="sm" onClick={() => add({ key: "", value: "" })}>
              <Plus data-icon="inline-start" />
              Add variable
            </Button>
            {vars && unused.length > 0 && (
              <VarMenu
                vars={unused}
                label="Same name, value from the project"
                onPick={(v) => add({ key: v, value: envRef(v) })}
                trigger={
                  <Button type="button" variant="outline" size="sm">
                    <Link2 data-icon="inline-start" />
                    Add from project
                  </Button>
                }
              />
            )}
          </div>
        </div>
      )}
      {description && <p className="px-1 text-xs text-muted-foreground">{description}</p>}
    </div>
  );
}

/** A value input; a lone project reference shows as a removable chip. */
function ValueField({
  value,
  onChange,
  invalid,
  refs,
}: {
  value: string;
  onChange: (v: string) => void;
  invalid: boolean;
  refs: boolean;
}) {
  const ref = refs ? soleRef(value) : null;
  if (ref)
    return (
      <div
        className={cn(
          "flex h-8 min-w-0 flex-1 items-center rounded-lg border border-input px-1.5 dark:bg-input/30",
          invalid && "border-destructive",
        )}
      >
        <span
          className={cn(
            "inline-flex min-w-0 items-center gap-1 rounded-md bg-muted py-0.5 pr-0.5 pl-1.5 font-mono text-xs",
            invalid && "bg-destructive/10 text-destructive",
          )}
          title={value.trim()}
        >
          <Link2 className="size-3 shrink-0" />
          <span className="truncate">{ref}</span>
          <button
            type="button"
            aria-label="Remove reference"
            onClick={() => onChange("")}
            className="rounded-sm p-0.5 text-muted-foreground hover:bg-background hover:text-foreground"
          >
            <X className="size-3" />
          </button>
        </span>
      </div>
    );
  const hidden = value === SECRET_MASK;
  return (
    <div className="relative min-w-0 flex-1">
      <Input
        aria-label="Value"
        placeholder="value"
        value={value}
        aria-invalid={invalid || undefined}
        onChange={(e) => onChange(e.target.value)}
        // A hidden value is replaced as a whole.
        onFocus={(e) => hidden && e.target.select()}
        spellCheck={false}
        className={cn("font-mono text-sm", hidden && "pr-7 text-muted-foreground")}
      />
      {hidden && (
        <Lock
          aria-label="Saved value, hidden"
          className="pointer-events-none absolute top-1/2 right-2.5 size-3.5 -translate-y-1/2 text-muted-foreground"
        />
      )}
    </div>
  );
}

function VarMenu({
  vars,
  label,
  onPick,
  trigger,
}: {
  vars: string[];
  label: string;
  onPick: (name: string) => void;
  trigger: ReactNode;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        {trigger}
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="max-h-72 min-w-48">
        <DropdownMenuLabel className="text-xs">{label}</DropdownMenuLabel>
        {vars.map((v) => (
          <DropdownMenuItem key={v} onSelect={() => onPick(v)} className="font-mono text-xs">
            {v}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
