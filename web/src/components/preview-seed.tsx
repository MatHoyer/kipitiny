import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, RotateCw, Trash2, TriangleAlert } from "lucide-react";
import { useId, useState } from "react";
import { toast } from "sonner";
import { ErrorText, Loading, Mono, Section } from "@/components/common";
import { useProjectGit } from "@/components/git-source";
import { ServiceIcon } from "@/components/service-icon";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { timeAgo } from "@/lib/format";
import { api, type AnonymizeRules, type PgTable, type Project, type SeedDatabase } from "../api";

/** A rule of the editor: a column's, or a whole table's when column is empty. */
type Row = { table: string; column: string; rule: string };

const tableRules: [string, string][] = [
  ["truncate", "Truncate (empty it)"],
  ["exclude", "Exclude (drop it)"],
];
const columnRules: [string, string][] = [
  ["fake:email", "Fake email"],
  ["fake:name", "Fake name"],
  ["fake:phone", "Fake phone"],
  ["fake:text", "Lorem ipsum"],
  ["hash", "Hash"],
  ["null", "Empty (null)"],
  ["fixed:", "Fixed"],
  ["keep", "Keep"],
];

export function toRows(rules: AnonymizeRules | null): Row[] {
  return Object.entries(rules ?? {}).flatMap(([table, r]) =>
    typeof r === "string" ? [{ table, column: "", rule: r }] : Object.entries(r).map(([column, rule]) => ({ table, column, rule })),
  );
}

export function toRules(rows: Row[]): AnonymizeRules {
  const out: AnonymizeRules = {};
  for (const { table, column, rule } of rows) {
    const t = table.trim();
    if (!t) continue;
    if (!column.trim()) out[t] = rule;
    else {
      const cols = typeof out[t] === "object" ? out[t] : {};
      out[t] = { ...cols, [column.trim()]: rule };
    }
  }
  return out;
}

// Same words as anonymize.Personal on the server.
const personalWords = new Set(
  `email mail phone mobile tel name firstname lastname surname fullname username address street city zip postal postcode
   birth birthday birthdate dob ssn iban card token secret password passwd ip lat lng latitude longitude`.split(/\s+/),
);

/** A column's name looks like it holds personal data. */
export function personal(column: string) {
  const words = column
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .toLowerCase()
    .split(/[^a-z0-9]+/);
  return words.some((w) => personalWords.has(w));
}

/** A first rule for a column that looks personal: a fake value of its kind for text, else empty. */
function suggest(column: string, type: string, nullable: boolean) {
  const w = column.toLowerCase();
  if (/char|text|citext/.test(type)) {
    if (/mail/.test(w)) return "fake:email";
    if (/phone|mobile|tel/.test(w)) return "fake:phone";
    if (/name/.test(w)) return "fake:name";
    if (/address|street|city/.test(w)) return "fake:text";
    return nullable ? "null" : "hash";
  }
  return nullable ? "null" : "keep";
}

/** The rules key of a listed table: schema.table outside postgres' public schema. */
function tableKey(db: SeedDatabase, t: PgTable) {
  return db.kind === "postgres" && t.schema !== "public" ? `${t.schema}.${t.name}` : t.name;
}

/** Each database's anonymize rules and the copy previews get, when they're seeded with anonymized data. */
export function PreviewSeedCard({ project, canEdit }: { project: Project; canEdit: boolean }) {
  const seed = useQuery({
    queryKey: ["preview-seed", project.id],
    queryFn: () => api.previewSeed(project.id),
    refetchInterval: (q) => (q.state.data?.some((d) => d.copy?.status === "running") ? 3_000 : 30_000),
  });
  const git = useProjectGit(project.id);
  const gitPath = git.data?.path;
  return (
    <Section
      title="Anonymized data"
      description={
        <>
          Previews seeded with <Mono>--seed</Mono> get a copy of each database's latest backup, anonymized with its rules in a throwaway server. A
          database without rules can't seed a preview; production data is never the fallback.
        </>
      }
    >
      {seed.error ? (
        <ErrorText error={seed.error} />
      ) : !seed.data ? (
        <Loading />
      ) : seed.data.length === 0 ? (
        <p className="text-sm text-muted-foreground">This project has no PostgreSQL, MySQL, MariaDB or MongoDB database.</p>
      ) : (
        <div className="space-y-6">
          {gitPath && (
            <p className="rounded-md border border-dashed px-3 py-2 text-sm text-muted-foreground">
              Set in <Mono>x-kipitiny.anonymize</Mono> of <Mono>{gitPath}</Mono>: change the rules in the repository.
            </p>
          )}
          {seed.data.map((db) => (
            <SeedDatabaseRules key={`${db.serviceId}:${JSON.stringify(db.rules)}`} project={project} db={db} canEdit={canEdit && !gitPath} />
          ))}
        </div>
      )}
    </Section>
  );
}

function SeedDatabaseRules({ project, db, canEdit }: { project: Project; db: SeedDatabase; canEdit: boolean }) {
  const qc = useQueryClient();
  const [rows, setRows] = useState(() => toRows(db.rules));
  const listId = useId();
  const tables = useQuery({
    queryKey: ["anonymize-tables", db.serviceId],
    queryFn: () => api.pgTables(db.serviceId, ""),
    enabled: db.kind !== "mongodb",
    retry: false,
  });
  const listed = (tables.data ?? []).filter((t) => t.kind === "table" || t.kind === "partitioned table");
  const saved = JSON.stringify(toRules(toRows(db.rules)));
  const dirty = JSON.stringify(toRules(rows)) !== saved || (db.rules === null && rows.length > 0);
  const save = useMutation({
    meta: { error: `Couldn't save ${db.name}'s rules` },
    mutationFn: (rules: AnonymizeRules | null) => api.setAnonymizeRules(project.id, db.name, rules),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["preview-seed", project.id] });
      toast.success(`${db.name}'s rules saved`, { description: "Its latest backup is anonymized again with them." });
    },
  });
  const retry = useMutation({
    meta: { error: "Couldn't start the anonymized copy" },
    mutationFn: () => api.anonymizeNow(project.id, db.name),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["preview-seed", project.id] }),
  });

  const ruled = toRules(rows);
  const unruled = listed.flatMap((t) => {
    const r = ruled[tableKey(db, t)];
    if (typeof r === "string") return [];
    return t.columns.filter((c) => personal(c.name) && !(r && c.name in r)).map((c) => ({ table: tableKey(db, t), column: c }));
  });
  const set = (i: number, patch: Partial<Row>) => setRows(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  const columnsOf = (table: string) => listed.find((t) => tableKey(db, t) === table)?.columns ?? [];

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="flex items-center gap-2 font-medium">
          <ServiceIcon service={{ kind: db.kind }} />
          {db.name}
        </h3>
        <CopyStatus db={db} onRetry={canEdit ? () => retry.mutate() : undefined} retrying={retry.isPending} />
      </div>
      {db.rules === null && rows.length === 0 ? (
        <p className="text-sm text-muted-foreground">No rules: previews can't be seeded with {db.name}.</p>
      ) : rows.length === 0 ? (
        <p className="text-sm text-muted-foreground">Holds nothing personal: previews get it as backed up.</p>
      ) : (
        <div className="space-y-2">
          <datalist id={`${listId}-tables`}>
            {listed.map((t) => (
              <option key={tableKey(db, t)} value={tableKey(db, t)} />
            ))}
          </datalist>
          {rows.map((r, i) => (
            <div key={i} className="grid gap-2 sm:grid-cols-[1fr_1fr_12rem_auto]">
              <Input
                aria-label="Table"
                placeholder={db.kind === "mongodb" ? "collection" : "table"}
                value={r.table}
                list={`${listId}-tables`}
                onChange={(e) => set(i, { table: e.target.value })}
                disabled={!canEdit}
              />
              <Input
                aria-label="Column"
                placeholder={db.kind === "mongodb" ? "field.path (empty: whole collection)" : "column (empty: whole table)"}
                value={r.column}
                list={`${listId}-cols-${i}`}
                onChange={(e) => {
                  // A whole table takes a table rule, a column a column rule.
                  const column = e.target.value;
                  const whole = !column.trim();
                  const tableRule = tableRules.some(([v]) => v === r.rule);
                  set(i, { column, rule: whole === tableRule ? r.rule : whole ? "truncate" : "fake:text" });
                }}
                disabled={!canEdit}
              />
              <datalist id={`${listId}-cols-${i}`}>
                {columnsOf(r.table).map((c) => (
                  <option key={c.name} value={c.name} />
                ))}
              </datalist>
              <div className="flex gap-2">
                <Select
                  value={r.rule.startsWith("fixed:") ? "fixed:" : r.rule}
                  onValueChange={(rule) => set(i, { rule })}
                  disabled={!canEdit}
                >
                  <SelectTrigger aria-label="Rule" className="w-full min-w-0">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {(r.column.trim() ? columnRules : tableRules).map(([v, label]) => (
                      <SelectItem key={v} value={v}>
                        {label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                {r.rule.startsWith("fixed:") && (
                  <Input
                    aria-label="Fixed value"
                    placeholder="fixed value"
                    value={r.rule.slice(6)}
                    onChange={(e) => set(i, { rule: "fixed:" + e.target.value })}
                    disabled={!canEdit}
                  />
                )}
              </div>
              {canEdit && (
                <Button type="button" variant="ghost" size="icon" aria-label="Remove rule" onClick={() => setRows(rows.filter((_, j) => j !== i))}>
                  <Trash2 />
                </Button>
              )}
            </div>
          ))}
        </div>
      )}
      {unruled.length > 0 && (
        <div className="flex flex-wrap items-center gap-2 rounded-md border border-amber-500/40 bg-amber-500/5 px-3 py-2 text-sm">
          <TriangleAlert className="size-4 text-amber-600" />
          <span className="flex-1">
            {unruled.length === 1 ? "This column looks" : "These columns look"} personal and {unruled.length === 1 ? "has" : "have"} no rule:{" "}
            {unruled.map((u) => `${u.table}.${u.column.name}`).join(", ")}
          </span>
          {canEdit && (
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() =>
                setRows([...rows, ...unruled.map((u) => ({ table: u.table, column: u.column.name, rule: suggest(u.column.name, u.column.type, u.column.nullable) }))])
              }
            >
              Add rules
            </Button>
          )}
        </div>
      )}
      {canEdit && (
        <div className="flex flex-wrap gap-2">
          <Button type="button" size="sm" variant="outline" onClick={() => setRows([...rows, { table: "", column: "", rule: "truncate" }])}>
            <Plus /> Rule
          </Button>
          {db.rules === null && rows.length === 0 && (
            <Button type="button" size="sm" variant="outline" onClick={() => save.mutate({})} disabled={save.isPending}>
              Holds nothing personal
            </Button>
          )}
          {db.rules !== null && (
            <Button type="button" size="sm" variant="ghost" onClick={() => save.mutate(null)} disabled={save.isPending}>
              Remove rules
            </Button>
          )}
          {dirty && (
            <>
              <Button type="button" size="sm" onClick={() => save.mutate(toRules(rows))} disabled={save.isPending}>
                Save rules
              </Button>
              <Button type="button" size="sm" variant="ghost" onClick={() => setRows(toRows(db.rules))}>
                Reset
              </Button>
            </>
          )}
        </div>
      )}
    </div>
  );
}

/** What previews get from the database now. */
function CopyStatus({ db, onRetry, retrying }: { db: SeedDatabase; onRetry?: () => void; retrying: boolean }) {
  if (db.rules === null) return null;
  const good = db.seeds && <span className="text-muted-foreground">Previews get the copy made {timeAgo(db.seeds.createdAt)}.</span>;
  if (!db.backup) return <span className="text-sm text-muted-foreground">No backup yet: back it up to make an anonymized copy.</span>;
  if (db.copy?.status === "running") return <span className="text-sm text-muted-foreground">Anonymizing the latest backup…</span>;
  if (db.copy?.status === "failed")
    return (
      <span className="flex flex-wrap items-center gap-2 text-sm">
        <span className="text-destructive">Anonymizing failed: {db.copy.error}</span>
        {good}
        {onRetry && (
          <Button type="button" size="sm" variant="outline" onClick={onRetry} disabled={retrying}>
            <RotateCw /> Retry
          </Button>
        )}
      </span>
    );
  return <span className="text-sm">{good ?? <span className="text-muted-foreground">No anonymized copy yet.</span>}</span>;
}
