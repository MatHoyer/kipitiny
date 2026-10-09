import { useQuery } from "@tanstack/react-query";
import { Code, List, Pencil, Plus, Trash2 } from "lucide-react";
import { useState, type ReactNode } from "react";
import { EnvEntryDialog, envKeyRe, type EnvContext } from "@/components/env-entry-dialog";
import { ManagerRefValue, TemplateValue } from "@/components/template-value";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import {
  dbRefValid,
  envRef,
  formatEnvRows,
  managerRef,
  parseEnvRows,
  refName,
  refsIn,
  type EnvReference,
  type EnvRow,
} from "@/lib/format";
import { cn } from "@/lib/utils";
import { api, SECRET_MASK, type DatabaseRef } from "../api";

type Mode = "list" | "raw";

/**
 * Edits environment variables and secrets as a list of fields or as
 * KEY=value text. Entries are added and edited in a dialog (EnvEntryDialog).
 * Secrets are write-only: their saved values read back masked. An entry that
 * is exactly one password manager reference ({{ pass://Vault/Item/field }})
 * holds no secret: it's listed on its own, showing where it points. With vars (the
 * project's shared entry names) and databases (its database services), values
 * can reference them as {{ project.NAME }} and {{ db.SERVICE.FIELD }}, alone or
 * within text. readOnly shows the entries without any way to change them.
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
  passwordManagers = false,
  readOnly = false,
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
  databases?: DatabaseRef[];
  /** Offers secrets from connected password managers. */
  passwordManagers?: boolean;
  readOnly?: boolean;
  className?: string;
}) {
  const [mode, setMode] = useState<Mode>("list");
  const [text, setText] = useState("");
  // The dialog adds (index null) or edits the entry at index. It keeps its
  // last state while closing; n remounts it fresh on each open.
  const [dialog, setDialog] = useState<{ open: boolean; index: number | null; n: number }>({
    open: false,
    index: null,
    n: 0,
  });
  const openDialog = (index: number | null) => setDialog((d) => ({ open: true, index, n: d.n + 1 }));

  const change = (next: EnvRow[]) => {
    onChange(next);
    if (mode === "raw") setText(formatEnvRows(next));
  };
  const switchMode = (m: Mode) => {
    if (!m || m === mode) return;
    if (m === "raw") setText(formatEnvRows(rows));
    setMode(m);
  };
  const refs = !!vars || !!databases;
  // Password manager references are checked by the manager on save.
  const known = (r: EnvReference) =>
    r.kind === "secret" ||
    (r.kind === "project"
      ? !!vars?.includes(r.name)
      : dbRefValid(databases, r.db, r.field));
  const fromManager = rows.map((r) => managerRef(r.value));
  const providers = useQuery({
    queryKey: ["secret-providers"],
    queryFn: api.secretProviders,
    enabled: passwordManagers || fromManager.some(Boolean),
  });
  // Offered only once this build has a password manager to connect.
  const ctx: EnvContext = { vars, secretVars, databases, passwordManagers: passwordManagers && !!providers.data?.length };
  // Unknown until the providers load; then a scheme no connected one resolves.
  const unresolved = (scheme: string) => !!providers.data && !providers.data.some((p) => p.connected && p.scheme === scheme);
  const counts = new Map<string, number>();
  for (const r of rows) counts.set(r.key, (counts.get(r.key) ?? 0) + 1);
  const editing = dialog.index != null ? (rows[dialog.index] ?? null) : null;

  const renderRow = (row: EnvRow, i: number) => {
    const badKey = !row.key || !envKeyRe.test(row.key) || (counts.get(row.key) ?? 0) > 1;
    const unknown = refs
      ? refsIn(row.value)
          .filter((r) => !known(r))
          .map(refName)
      : [];
    const saved = row.secret && row.value === SECRET_MASK;
    const mref = fromManager[i];
    const offline = !!mref && unresolved(mref.scheme);
    return (
      <li key={i} className="group/row">
        <div
          className={cn(
            "flex min-h-11 items-center gap-3 rounded-lg border px-3 py-1.5 transition-colors",
            !readOnly && "cursor-pointer hover:bg-muted/50",
            (badKey || unknown.length > 0 || offline) && "border-destructive/60",
          )}
          onClick={() => !readOnly && openDialog(i)}
        >
          <span className={cn("w-2/5 shrink-0 truncate font-mono text-sm", badKey && "text-destructive")}>
            {row.key || "unnamed"}
          </span>
          <span className="flex min-w-0 flex-1 items-center">
            {mref ? (
              <ManagerRefValue scheme={mref.scheme} path={mref.path} invalid={offline} />
            ) : saved ? (
              <span className="font-mono text-xs tracking-widest text-muted-foreground" title="Saved secret">
                ••••••••
              </span>
            ) : (
              <TemplateValue value={row.value} maskText={row.secret} invalid={refs ? (r) => !known(r) : undefined} />
            )}
          </span>
          {!readOnly && (
            <span className="flex shrink-0 gap-0.5">
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                title="Edit"
                aria-label={`Edit ${row.key || "entry"}`}
                onClick={(e) => {
                  e.stopPropagation();
                  openDialog(i);
                }}
              >
                <Pencil />
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                title="Remove"
                aria-label={`Remove ${row.key || "entry"}`}
                className="text-muted-foreground hover:text-destructive"
                onClick={(e) => {
                  e.stopPropagation();
                  change(rows.filter((_, j) => j !== i));
                }}
              >
                <Trash2 />
              </Button>
            </span>
          )}
        </div>
        {(badKey || unknown.length > 0 || offline) && (
          <p className="px-1 pt-1 text-xs text-destructive">
            {offline && !badKey
              ? `No connected password manager resolves ${mref.scheme}:// (Integrations › Password managers).`
              : !row.key
              ? "Missing name."
              : (counts.get(row.key) ?? 0) > 1
                ? "Duplicate name."
                : badKey
                  ? "Letters, digits and _; not starting with a digit."
                  : `Unknown reference ${unknown.join(", ")}.`}
          </p>
        )}
      </li>
    );
  };

  const kind = (r: EnvRow, i: number) => (fromManager[i] ? "manager" : r.secret ? "secret" : "variable");
  const count = (k: string) => rows.filter((r, i) => kind(r, i) === k).length;
  const varCount = count("variable");
  const managerCount = count("manager");
  const secretCount = count("secret");

  return (
    <div className={cn("space-y-2", className)}>
      {showLabel && <span className="block text-sm font-medium">{label}</span>}
      <div className="flex items-center justify-between gap-2">
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
        {!readOnly && (
          <Button type="button" size="sm" onClick={() => openDialog(null)}>
            <Plus data-icon="inline-start" />
            Add
          </Button>
        )}
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
          disabled={readOnly}
          spellCheck={false}
          className="font-mono text-sm"
        />
      ) : rows.length === 0 ? (
        <p className="rounded-lg border border-dashed px-3 py-6 text-center text-sm text-muted-foreground">
          {readOnly ? "No variables or secrets." : "No variables or secrets yet."}
        </p>
      ) : (
        <div className="space-y-4">
          {varCount > 0 && (
            <Group title="Variables" hint="Readable values." count={varCount}>
              {rows.map((r, i) => kind(r, i) === "variable" && renderRow(r, i))}
            </Group>
          )}
          {managerCount > 0 && (
            <Group title="From password manager" hint="Fetched at each deploy, never stored." count={managerCount}>
              {rows.map((r, i) => kind(r, i) === "manager" && renderRow(r, i))}
            </Group>
          )}
          {secretCount > 0 && (
            <Group title="Secrets" hint="Write-only once saved." count={secretCount}>
              {rows.map((r, i) => kind(r, i) === "secret" && renderRow(r, i))}
            </Group>
          )}
        </div>
      )}
      {description && <p className="px-1 text-xs text-muted-foreground">{description}</p>}
      {!readOnly && (
        <EnvEntryDialog
          key={dialog.n}
          open={dialog.open}
          onOpenChange={(open) => setDialog((d) => ({ ...d, open }))}
          entry={editing}
          taken={rows.filter((_, j) => j !== dialog.index).map((r) => r.key)}
          ctx={ctx}
          onDone={(added) =>
            change(
              dialog.index != null
                ? rows.map((r, j) => (j === dialog.index ? added[0] : r))
                : [...rows, ...added],
            )
          }
        />
      )}
    </div>
  );
}

function Group({ title, hint, count, children }: { title: string; hint: string; count: number; children: ReactNode }) {
  return (
    <section className="space-y-2">
      <div className="flex items-baseline gap-2">
        <h4 className="text-xs font-medium tracking-wide text-muted-foreground uppercase">
          {title}
          <span className="ml-1.5 font-normal">{count}</span>
        </h4>
        <span className="truncate text-xs text-muted-foreground">{hint}</span>
      </div>
      <ul className="space-y-1.5">{children}</ul>
    </section>
  );
}
