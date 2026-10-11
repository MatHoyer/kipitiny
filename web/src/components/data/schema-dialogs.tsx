import { Plus, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { ChangeResult, NewColumn } from "../../api";
import { ReviewDialog } from "./changes";
import type { SqlDialect } from "./sql-editor";

type TypeGroup = { label: string; types: [type: string, hint: string][] };

/** The types offered per dialect, grouped; Custom… takes any other. The first is a new table's id. */
const typeGroups: Record<"postgres" | "mysql", TypeGroup[]> = {
  postgres: [
    {
      label: "Numbers",
      types: [
        ["bigserial", "auto-increment id"],
        ["bigint", "64-bit integer"],
        ["integer", "32-bit integer"],
        ["smallint", "16-bit integer"],
        ["numeric(10,2)", "exact decimal, money"],
        ["double precision", "floating point"],
      ],
    },
    {
      label: "Text",
      types: [
        ["text", "any length"],
        ["varchar(255)", "up to 255 characters"],
        ["uuid", "UUID"],
      ],
    },
    {
      label: "Date and time",
      types: [
        ["timestamp with time zone", "a moment, the usual choice"],
        ["timestamp", "without time zone"],
        ["date", "a day"],
        ["time", "time of day"],
        ["interval", "a duration"],
      ],
    },
    {
      label: "Other",
      types: [
        ["boolean", "true or false"],
        ["jsonb", "JSON, indexable"],
        ["bytea", "bytes"],
        ["inet", "IP address"],
        ["text[]", "array of text"],
      ],
    },
  ],
  mysql: [
    {
      label: "Numbers",
      types: [
        ["bigint auto_increment", "auto-increment id"],
        ["bigint", "64-bit integer"],
        ["int", "32-bit integer"],
        ["smallint", "16-bit integer"],
        ["decimal(10,2)", "exact decimal, money"],
        ["double", "floating point"],
      ],
    },
    {
      label: "Text",
      types: [
        ["varchar(255)", "up to 255 characters"],
        ["text", "up to 64 KiB"],
        ["longtext", "up to 4 GiB"],
        ["char(36)", "fixed length, a UUID"],
      ],
    },
    {
      label: "Date and time",
      types: [
        ["datetime", "a date and time"],
        ["timestamp", "converted to UTC"],
        ["date", "a day"],
        ["time", "time of day"],
      ],
    },
    {
      label: "Other",
      types: [
        ["boolean", "true or false"],
        ["json", "JSON document"],
        ["blob", "bytes"],
      ],
    },
  ],
};

const CUSTOM = "__custom";

/** A column's type: one of the usual ones, or Custom… to type any other. */
function TypePicker({ groups, value, onChange, label }: { groups: TypeGroup[]; value: string; onChange: (t: string) => void; label: string }) {
  const known = groups.some((g) => g.types.some(([t]) => t === value));
  const [custom, setCustom] = useState(!known);
  return (
    <span className="flex min-w-0 flex-col gap-1">
      <Select
        value={custom ? CUSTOM : value}
        onValueChange={(v) => {
          setCustom(v === CUSTOM);
          if (v !== CUSTOM) onChange(v);
        }}
      >
        <SelectTrigger size="sm" aria-label={label} className="w-full min-w-0 font-mono text-xs">
          {/* The type alone: its hint is for the list. */}
          <SelectValue>{custom ? "Custom…" : value}</SelectValue>
        </SelectTrigger>
        <SelectContent className="max-h-80">
          {groups.map((g) => (
            <SelectGroup key={g.label}>
              <SelectLabel className="text-xs">{g.label}</SelectLabel>
              {g.types.map(([t, hint]) => (
                <SelectItem key={t} value={t} className="font-mono text-xs">
                  {t}
                  <span className="ml-auto pl-3 font-sans text-muted-foreground">{hint}</span>
                </SelectItem>
              ))}
            </SelectGroup>
          ))}
          <SelectGroup>
            <SelectLabel className="text-xs">Anything else</SelectLabel>
            <SelectItem value={CUSTOM} className="text-xs">
              Custom…
            </SelectItem>
          </SelectGroup>
        </SelectContent>
      </Select>
      {custom && (
        <input
          autoFocus
          aria-label={`${label}, custom`}
          placeholder="e.g. varchar(32)"
          spellCheck={false}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className={inputClass}
        />
      )}
    </span>
  );
}

const inputClass =
  "h-8 min-w-0 rounded-md border bg-background px-2 font-mono text-xs outline-none placeholder:text-muted-foreground/70 focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50";

/** Designs a table, then shows its CREATE TABLE for review before running it. */
export function NewTableDialog({
  open,
  onOpenChange,
  dialect,
  defaultSchema,
  run,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  dialect: SqlDialect;
  /** PostgreSQL's schema for the table; MySQL's is the database. */
  defaultSchema: string;
  run: (schema: string, name: string, columns: NewColumn[], apply: boolean) => Promise<ChangeResult>;
  onCreated: (schema: string, name: string) => void;
}) {
  const groups = typeGroups[dialect === "postgres" ? "postgres" : "mysql"];
  const first = (): NewColumn[] => [{ name: "id", type: groups[0].types[0][0], nullable: false, primaryKey: true }];
  const [name, setName] = useState("");
  const [schema, setSchema] = useState(defaultSchema);
  const [columns, setColumns] = useState<NewColumn[]>(first);
  const [reviewing, setReviewing] = useState(false);
  const set = (i: number, patch: Partial<NewColumn>) => setColumns(columns.map((c, k) => (k === i ? { ...c, ...patch } : c)));
  const close = (o: boolean) => {
    onOpenChange(o);
    if (!o) {
      setName("");
      setSchema(defaultSchema);
      setColumns(first());
    }
  };
  const ready = name.trim() !== "" && columns.length > 0 && columns.every((c) => c.name.trim() && c.type.trim());

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className="flex max-h-[85vh] flex-col sm:max-w-3xl">
        <form
          className="contents"
          onSubmit={(e) => {
            e.preventDefault();
            if (ready) setReviewing(true);
          }}
        >
          <DialogHeader>
            <DialogTitle>New table</DialogTitle>
            <DialogDescription>Its columns, with the database&apos;s own types. You review the CREATE TABLE before it runs.</DialogDescription>
          </DialogHeader>
          <div className="min-h-0 space-y-4 overflow-y-auto">
            <div className="flex gap-2">
              {dialect === "postgres" && (
                <FloatingInput label="Schema" autoComplete="off" spellCheck={false} value={schema} onChange={(e) => setSchema(e.target.value)} inputClassName="font-mono" className="w-40" />
              )}
              <FloatingInput label="Name" autoFocus autoComplete="off" spellCheck={false} value={name} onChange={(e) => setName(e.target.value)} inputClassName="font-mono" className="flex-1" />
            </div>
            <div className="space-y-1.5">
              <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,0.8fr)_auto_auto_auto] items-center gap-1.5 px-0.5 text-xs text-muted-foreground">
                <span>Column</span>
                <span>Type</span>
                <span>Default</span>
                <span title="Part of the primary key">Key</span>
                <span title="Can hold NULL">Null</span>
                <span className="w-7" />
              </div>
              {columns.map((c, i) => (
                <div key={i} className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,0.8fr)_auto_auto_auto] items-start gap-1.5 [&>[role=checkbox]]:mt-2">
                  <input aria-label={`Column ${i + 1} name`} placeholder="name" spellCheck={false} value={c.name} onChange={(e) => set(i, { name: e.target.value })} className={inputClass} />
                  <TypePicker groups={groups} label={`Column ${i + 1} type`} value={c.type} onChange={(type) => set(i, { type })} />
                  <input
                    aria-label={`Column ${i + 1} default`}
                    placeholder="none"
                    spellCheck={false}
                    value={c.default ?? ""}
                    onChange={(e) => set(i, { default: e.target.value })}
                    className={inputClass}
                  />
                  <Checkbox
                    aria-label={`Column ${i + 1} in the primary key`}
                    checked={c.primaryKey}
                    onCheckedChange={(v) => set(i, { primaryKey: v === true, nullable: v === true ? false : c.nullable })}
                    className="mx-2"
                  />
                  <Checkbox
                    aria-label={`Column ${i + 1} can be NULL`}
                    checked={c.nullable}
                    disabled={c.primaryKey}
                    onCheckedChange={(v) => set(i, { nullable: v === true })}
                    className="mx-2"
                  />
                  <Button type="button" variant="ghost" size="icon-xs" title="Remove this column" disabled={columns.length === 1} onClick={() => setColumns(columns.filter((_, k) => k !== i))}>
                    <Trash2 />
                  </Button>
                </div>
              ))}
              <Button type="button" variant="ghost" size="xs" onClick={() => setColumns([...columns, { name: "", type: dialect === "postgres" ? "text" : "varchar(255)", nullable: true, primaryKey: false }])}>
                <Plus /> Column
              </Button>
            </div>
          </div>
          <DialogFooter>
            <Button type="submit" disabled={!ready}>
              Review
            </Button>
          </DialogFooter>
        </form>
        <ReviewDialog
          open={reviewing}
          onOpenChange={setReviewing}
          title="Create table"
          action="Create table"
          atomic="An empty table, until you add rows."
          run={(apply) => run(schema.trim(), name.trim(), columns.map((c) => ({ ...c, default: c.default?.trim() || undefined })), apply)}
          onSaved={() => {
            toast.success(`${name.trim()} created`);
            onCreated(schema.trim(), name.trim());
            close(false);
          }}
        />
      </DialogContent>
    </Dialog>
  );
}

/** Names a collection, then shows the createCollection call for review. */
export function NewCollectionDialog({
  open,
  onOpenChange,
  run,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  run: (name: string, apply: boolean) => Promise<ChangeResult>;
  onCreated: (name: string) => void;
}) {
  const [name, setName] = useState("");
  const [reviewing, setReviewing] = useState(false);
  const close = (o: boolean) => {
    onOpenChange(o);
    if (!o) setName("");
  };
  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className="sm:max-w-md">
        <form
          className="contents"
          onSubmit={(e) => {
            e.preventDefault();
            if (name.trim()) setReviewing(true);
          }}
        >
          <DialogHeader>
            <DialogTitle>New collection</DialogTitle>
            <DialogDescription>An empty collection in this database.</DialogDescription>
          </DialogHeader>
          <FloatingInput
            label="Name"
            autoFocus
            autoComplete="off"
            spellCheck={false}
            value={name}
            onChange={(e) => setName(e.target.value)}
            inputClassName="font-mono"
            description="Letters, digits, _ . and -, starting with a letter or _."
          />
          <DialogFooter>
            <Button type="submit" disabled={!name.trim()}>
              Review
            </Button>
          </DialogFooter>
        </form>
        <ReviewDialog
          open={reviewing}
          onOpenChange={setReviewing}
          title="Create collection"
          action="Create collection"
          atomic="An empty collection, until you add documents."
          run={(apply) => run(name.trim(), apply)}
          onSaved={() => {
            toast.success(`${name.trim()} created`);
            onCreated(name.trim());
            close(false);
          }}
        />
      </DialogContent>
    </Dialog>
  );
}

/** Drops a table or collection after a review where its name must be typed. */
export function DropButton({
  what,
  name,
  run,
  onDropped,
  note,
}: {
  /** "table", "view", "collection"... */
  what: string;
  name: string;
  run: (apply: boolean) => Promise<ChangeResult>;
  onDropped: () => void;
  /** What dropping takes with it. */
  note: string;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button variant="outline" size="icon-sm" className="hover:text-destructive" title={`Drop this ${what}`} aria-label={`Drop ${what}`} onClick={() => setOpen(true)}>
        <Trash2 />
      </Button>
      <ReviewDialog
        open={open}
        onOpenChange={setOpen}
        title={`Drop ${what} ${name}?`}
        action={`Drop ${what}`}
        confirmName={name}
        atomic={note}
        run={run}
        onSaved={() => {
          toast.success(`${name} dropped`);
          onDropped();
        }}
      />
    </>
  );
}
