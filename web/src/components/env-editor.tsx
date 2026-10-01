import { Braces, Code, Database, KeyRound, Link2, List, Plus, Trash2, Vault, X } from "lucide-react";
import { useState, type ReactNode } from "react";
import { PostgresIcon } from "@/components/brand-icons";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import {
  dbFields,
  dbRef,
  envRef,
  formatEnvRows,
  parseEnvRows,
  refName,
  refsIn,
  soleRef,
  type EnvReference,
  type EnvRow,
} from "@/lib/format";
import { cn } from "@/lib/utils";
import { SECRET_MASK } from "../api";

const keyRe = /^[A-Za-z_][A-Za-z0-9_]*$/;

type Mode = "list" | "raw";

/**
 * Edits environment variables and secrets as a list of fields or as
 * KEY=value text. Secrets are write-only: their saved values read back
 * masked. With vars (the project's shared entry names) and databases (its
 * postgres services), values can reference them as {{ project.NAME }} and
 * {{ db.SERVICE.FIELD }}, picked from menus instead of typed.
 */
export function EnvEditor({
  label = "Environment",
  showLabel = true,
  description,
  rows,
  onChange,
  vars,
  secretVars = [],
  databases,
  className,
}: {
  label?: string;
  /** False when the surrounding card already names it. */
  showLabel?: boolean;
  description?: ReactNode;
  rows: EnvRow[];
  onChange: (rows: EnvRow[]) => void;
  vars?: string[];
  /** The vars that are secrets: offered to secrets, the others to variables. */
  secretVars?: string[];
  /** The project's database names; enables "Connect database". */
  databases?: string[];
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
  const add = (...added: EnvRow[]) => {
    setFocus(rows.length);
    onChange([...rows, ...added]);
  };
  const refs = !!vars || !!databases;
  // Password manager references are checked by the manager on save.
  const known = (r: EnvReference) =>
    r.kind === "secret" ||
    (r.kind === "project"
      ? !!vars?.includes(r.name)
      : !!databases?.includes(r.db) && (dbFields as readonly string[]).includes(r.field));
  // A name for a database's entry: the usual one, or prefixed by the database
  // when taken (several databases, one service).
  const dbEntryName = (db: string, key: string) =>
    rows.some((r) => r.key === key) ? `${db.toUpperCase().replaceAll("-", "_")}_${key}` : key;
  const connect = (db: string, fields: [string, string][]) =>
    add(...fields.map(([key, field]) => ({ key: dbEntryName(db, key), value: dbRef(db, field), secret: true })));
  const counts = new Map<string, number>();
  for (const r of rows) counts.set(r.key, (counts.get(r.key) ?? 0) + 1);
  // Project entries, split like the rows: a service variable uses a project
  // variable, a service secret a project secret.
  const projectVars = vars?.filter((v) => !secretVars.includes(v)) ?? [];
  const projectSecrets = vars?.filter((v) => secretVars.includes(v)) ?? [];
  const unused = (names: string[]) => names.filter((v) => !rows.some((r) => r.key === v));

  const renderRow = (row: EnvRow, i: number) => {
    const badKey = !!row.key && (!keyRe.test(row.key) || (counts.get(row.key) ?? 0) > 1);
    const unknown = refs
      ? refsIn(row.value)
          .filter((r) => !known(r))
          .map(refName)
      : [];
    // A service secret may use a project secret or a database credential; a
    // variable a project variable.
    const sections = row.secret
      ? [
          { label: "Project secrets", items: projectSecrets.map((v) => ({ label: v, key: v, value: envRef(v) })) },
          ...(databases ?? []).map((db) => ({
            label: `Database ${db}`,
            items: dbFields.map((f) => ({
              label: f,
              key: f === "URL" ? "DATABASE_URL" : `PG${f}`,
              value: dbRef(db, f),
            })),
          })),
        ]
      : [{ label: "Project variables", items: projectVars.map((v) => ({ label: v, key: v, value: envRef(v) })) }];
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
            secret={row.secret}
            onChange={(value) => update(i, { value })}
            invalid={unknown.length > 0}
            refs={refs}
          />
          {sections.some((s) => s.items.length > 0) && (
            <RefMenu
              sections={sections}
              onPick={(item) => update(i, { key: row.key || item.key, value: item.value })}
              trigger={
                <Button type="button" variant="ghost" size="icon" aria-label="Use a reference">
                  <Braces />
                </Button>
              }
            />
          )}
          <Button
            type="button"
            variant="ghost"
            size="icon"
            aria-label={`Remove ${row.key || (row.secret ? "secret" : "variable")}`}
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
              : `Unknown reference ${unknown.join(", ")}.`}
          </p>
        )}
      </div>
    );
  };

  return (
    <div className={cn("space-y-2", className)}>
      <div className="flex items-center justify-between gap-2">
        <span className="text-sm font-medium">{showLabel && label}</span>
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
          placeholder={`NODE_ENV=production${vars ? `\nAPI_URL=${envRef("API_URL")}` : ""}\n# secrets\nAPI_TOKEN=…`}
          spellCheck={false}
          className="font-mono text-sm"
        />
      ) : (
        <div className="space-y-4">
          <Group title="Variables" hint="Readable values." count={rows.filter((r) => !r.secret).length}>
            {rows.map((r, i) => !r.secret && renderRow(r, i))}
            <div className="flex flex-wrap gap-2">
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => add({ key: "", value: "", secret: false })}
              >
                <Plus data-icon="inline-start" />
                Add variable
              </Button>
              <FromProject
                names={unused(projectVars)}
                onPick={(v) => add({ key: v, value: envRef(v), secret: false })}
              />
            </div>
          </Group>
          <Group title="Secrets" hint="Write-only once saved." count={rows.filter((r) => r.secret).length}>
            {rows.map((r, i) => r.secret && renderRow(r, i))}
            <div className="flex flex-wrap gap-2">
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => add({ key: "", value: "", secret: true })}
              >
                <KeyRound data-icon="inline-start" />
                Add secret
              </Button>
              <FromProject
                names={unused(projectSecrets)}
                onPick={(v) => add({ key: v, value: envRef(v), secret: true })}
              />
              {databases && databases.length > 0 && (
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <Button type="button" variant="outline" size="sm">
                      <Database data-icon="inline-start" />
                      Connect database
                    </Button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="start" className="max-h-80 min-w-56">
                    {databases.map((db, n) => (
                      <div key={db}>
                        {n > 0 && <DropdownMenuSeparator />}
                        <DropdownMenuLabel className="text-xs">{db}</DropdownMenuLabel>
                        <DropdownMenuItem onSelect={() => connect(db, [["DATABASE_URL", "URL"]])}>
                          <span className="font-mono text-xs">DATABASE_URL</span>
                        </DropdownMenuItem>
                        <DropdownMenuItem
                          onSelect={() =>
                            connect(
                              db,
                              dbFields.filter((f) => f !== "URL").map((f) => [`PG${f}`, f]),
                            )
                          }
                        >
                          <span className="font-mono text-xs">PGHOST, PGPORT, PGUSER…</span>
                        </DropdownMenuItem>
                      </div>
                    ))}
                  </DropdownMenuContent>
                </DropdownMenu>
              )}
            </div>
          </Group>
        </div>
      )}
      {description && <p className="px-1 text-xs text-muted-foreground">{description}</p>}
    </div>
  );
}

/**
 * A value input; a lone reference shows as a removable chip. A secret is
 * typed blind, and its saved value only shows as the mask.
 */
function ValueField({
  value,
  secret,
  onChange,
  invalid,
  refs,
}: {
  value: string;
  secret: boolean;
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
          {ref.kind === "db" ? (
            <PostgresIcon className="size-3 shrink-0 text-[#4169E1]" />
          ) : ref.kind === "secret" ? (
            <Vault className="size-3 shrink-0" />
          ) : (
            <Link2 className="size-3 shrink-0" />
          )}
          <span className="truncate">
            {ref.kind === "db" ? `${ref.db}.${ref.field}` : ref.kind === "secret" ? ref.ref : ref.name}
          </span>
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
  const saved = secret && value === SECRET_MASK;
  return (
    <div className="relative min-w-0 flex-1">
      <Input
        aria-label="Value"
        type={secret ? "password" : "text"}
        autoComplete={secret ? "new-password" : "off"}
        placeholder={secret ? "secret value" : "value"}
        value={value}
        aria-invalid={invalid || undefined}
        onChange={(e) => onChange(e.target.value)}
        // A saved secret is replaced as a whole.
        onFocus={(e) => saved && e.target.select()}
        spellCheck={false}
        title={saved ? "Saved secret: type to replace it" : undefined}
        className={cn("font-mono text-sm", saved && "text-muted-foreground")}
      />
    </div>
  );
}

function Group({ title, hint, count, children }: { title: string; hint: string; count: number; children: ReactNode }) {
  return (
    <section className="space-y-2">
      <div className="flex items-baseline gap-2">
        <h4 className="text-xs font-medium tracking-wide text-muted-foreground uppercase">
          {title}
          {count > 0 && <span className="ml-1.5 font-normal">{count}</span>}
        </h4>
        <span className="truncate text-xs text-muted-foreground">{hint}</span>
      </div>
      {children}
    </section>
  );
}

/** Adds an entry with the same name, its value taken from the project. */
function FromProject({ names, onPick }: { names: string[]; onPick: (name: string) => void }) {
  if (names.length === 0) return null;
  return (
    <RefMenu
      sections={[
        { label: "Same name, value from the project", items: names.map((v) => ({ label: v, key: v, value: v })) },
      ]}
      onPick={(item) => onPick(item.key)}
      trigger={
        <Button type="button" variant="outline" size="sm">
          <Link2 data-icon="inline-start" />
          Add from project
        </Button>
      }
    />
  );
}

type RefItem = { label: string; key: string; value: string };

function RefMenu({
  sections,
  onPick,
  trigger,
}: {
  sections: { label: string; items: RefItem[] }[];
  onPick: (item: RefItem) => void;
  trigger: ReactNode;
}) {
  const shown = sections.filter((s) => s.items.length > 0);
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>{trigger}</DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="max-h-80 min-w-48">
        {shown.map((s, n) => (
          <div key={s.label}>
            {n > 0 && <DropdownMenuSeparator />}
            <DropdownMenuLabel className="text-xs">{s.label}</DropdownMenuLabel>
            {s.items.map((item) => (
              <DropdownMenuItem key={item.label} onSelect={() => onPick(item)} className="font-mono text-xs">
                {item.label}
              </DropdownMenuItem>
            ))}
          </div>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
