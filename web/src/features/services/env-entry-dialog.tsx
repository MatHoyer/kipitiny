import { ChevronLeft, Database, KeyRound, Variable, Vault } from "lucide-react";
import { useState, type FormEvent, type ReactNode } from "react";
import { Mono } from "@/components/common";
import { ChoiceTile } from "@kipit-labs/kipitidy/choice-tile";
import { Button } from "@kipit-labs/kipitidy/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@kipit-labs/kipitidy/dialog";
import { FloatingInput } from "@kipit-labs/kipitidy/floating-input";
import { Segmented, SegmentedItem } from "@kipit-labs/kipitidy/segmented";
import { dbFields, dbRef, dbRefValid, envRef, managerRef, refName, refsIn, type EnvReference, type EnvRow } from "@/lib/format";
import { SECRET_MASK, type DatabaseRef } from "../../api";
import { DatabaseForm } from "@/features/services/database-env-form";
import { ManagerSourceFields, useManagerSource } from "@/features/services/password-manager-source";
import { TemplateInput } from "@/features/services/template-input";

export const envKeyRe = /^[A-Za-z_][A-Za-z0-9_]*$/;

/** An env name for a password manager field: API Keys + token → API_KEYS_TOKEN. */
const fieldEntryName = (item: string, field: string) =>
  `${item}_${field.split(".").pop()}`
    .toUpperCase()
    .replace(/[^A-Z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "")
    .replace(/^(\d)/, "_$1");

/** What the env editor offers references to. */
export type EnvContext = {
  /** The project's shared entry names; undefined when references aren't checked. */
  vars?: string[];
  /** The vars that are secrets. */
  secretVars: string[];
  /** The project's database names. */
  databases?: DatabaseRef[];
  /** Offers secrets from connected password managers. */
  passwordManagers: boolean;
};

/** What an entry is: a readable value, a write-only one, or a password manager reference. */
type Kind = "variable" | "secret" | "manager";

const entryKind = (e: EnvRow): Kind => (managerRef(e.value) ? "manager" : e.secret ? "secret" : "variable");

type Step = { step: "pick" } | { step: "entry"; kind: Kind } | { step: "database" };

/**
 * Adds or edits env entries in steps: pick a variable, a secret, a password
 * manager secret or a database connection, then fill it in. A variable or
 * secret is typed and may embed references ({{ project.NAME }},
 * {{ db.SERVICE.FIELD }}); a password manager one is picked from its vaults.
 * entry set edits that entry, skipping the first step.
 */
export function EnvEntryDialog({
  open,
  onOpenChange,
  entry,
  taken,
  ctx,
  onDone,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** The entry being edited; null adds. */
  entry: EnvRow | null;
  /** Names already used by the other entries. */
  taken: string[];
  ctx: EnvContext;
  /** The entries to save: the edited one, or the added ones. */
  onDone: (rows: EnvRow[]) => void;
}) {
  const [step, setStep] = useState<Step>({ step: "pick" });
  const current: Step = entry ? { step: "entry", kind: entryKind(entry) } : step;
  const back = entry ? undefined : () => setStep({ step: "pick" });
  const done = (rows: EnvRow[]) => {
    onDone(rows);
    onOpenChange(false);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        {current.step === "pick" && (
          <>
            <DialogHeader>
              <DialogTitle>Add to the environment</DialogTitle>
              <DialogDescription>What should the service receive?</DialogDescription>
            </DialogHeader>
            <div className="grid gap-3 sm:grid-cols-2">
              <ChoiceTile
                icon={<Variable />}
                title="Variable"
                description="A readable value."
                onClick={() => setStep({ step: "entry", kind: "variable" })}
              />
              <ChoiceTile
                icon={<KeyRound />}
                title="Secret"
                description="Write-only once saved."
                onClick={() => setStep({ step: "entry", kind: "secret" })}
              />
              {ctx.passwordManagers && (
                <ChoiceTile
                  icon={<Vault />}
                  title="From password manager"
                  description="A secret that stays in your vault."
                  onClick={() => setStep({ step: "entry", kind: "manager" })}
                />
              )}
              {!!ctx.databases?.length && (
                <ChoiceTile
                  icon={<Database />}
                  title="Connect database"
                  description="Credentials of a project database."
                  onClick={() => setStep({ step: "database" })}
                />
              )}
            </div>
          </>
        )}
        {current.step === "entry" && (
          <EntryForm
            key={entry ? `edit-${entry.key}` : current.kind}
            entry={entry}
            kind={current.kind}
            taken={taken}
            ctx={ctx}
            onBack={back}
            onDone={(row) => done([row])}
          />
        )}
        {current.step === "database" && ctx.databases && (
          <DatabaseForm databases={ctx.databases} taken={taken} onBack={back!} onDone={done} />
        )}
      </DialogContent>
    </Dialog>
  );
}

function EntryForm({
  entry,
  kind: initialKind,
  taken,
  ctx,
  onBack,
  onDone,
}: {
  entry: EnvRow | null;
  kind: Kind;
  taken: string[];
  ctx: EnvContext;
  onBack?: () => void;
  onDone: (row: EnvRow) => void;
}) {
  const [kind, setKind] = useState(initialKind);
  const secret = kind === "secret";
  const fromProvider = kind === "manager";
  // A saved secret reads back masked: left empty, it keeps its value.
  const saved = !!entry?.secret && entry.value === SECRET_MASK;
  const initialRef = entry ? managerRef(entry.value) : null;
  const ms = useManagerSource(initialRef?.scheme ?? "", ctx.passwordManagers || fromProvider);
  const source = ms.source;
  const [name, setName] = useState(entry?.key ?? "");
  const [value, setValue] = useState(saved || initialRef ? "" : (entry?.value ?? ""));
  const [path, setPath] = useState(initialRef?.path.join("/") ?? "");

  const key = name.trim();
  const badName = !!key && !envKeyRe.test(key);
  const dupName = taken.includes(key);
  const known = (r: EnvReference) =>
    r.kind === "secret" ||
    // Checked when deploying: the app needs a domain.
    r.kind === "app" ||
    (r.kind === "project"
      ? !!ctx.vars?.includes(r.name)
      : dbRefValid(ctx.databases, r.db, r.field));
  const checked = !!ctx.vars || !!ctx.databases;
  const unknown = !fromProvider && checked ? refsIn(value).filter((r) => !known(r)).map(refName) : [];
  // Turning a saved secret into a variable would reveal it: it needs a new value.
  const needsValue = saved && !secret && !fromProvider && !value;
  const finalValue = fromProvider ? `{{ ${source}://${path.trim().replace(/^\/+/, "")} }}` : value;
  const valid =
    !!key && !badName && !dupName && unknown.length === 0 && !needsValue && (!fromProvider || (!!source && !!path.trim()));

  // Variables use the project's variables; secrets also its secrets and the
  // databases' credentials.
  const projectVars = ctx.vars?.filter((v) => !ctx.secretVars.includes(v)) ?? [];
  const projectSecrets = ctx.vars?.filter((v) => ctx.secretVars.includes(v)) ?? [];
  const sections = [
    { label: "Project variables", items: projectVars.map((v) => ({ label: v, value: envRef(v) })) },
    ...(secret
      ? [
          { label: "Project secrets", items: projectSecrets.map((v) => ({ label: v, value: envRef(v) })) },
          ...(ctx.databases ?? []).map((db) => ({
            label: `Database ${db.name}`,
            items: dbFields[db.kind].map((f) => ({ label: f, value: dbRef(db.name, f) })),
          })),
        ]
      : []),
  ].filter((s) => s.items.length > 0);

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    // Rendered in a portal, but React still bubbles the submit to the
    // surrounding form.
    e.stopPropagation();
    if (!valid) return;
    onDone({ key, secret, value: saved && !fromProvider && !value ? SECRET_MASK : finalValue });
  };

  return (
    <form onSubmit={onSubmit} className="contents">
      <DialogHeader>
        <DialogTitle className="flex items-center gap-2 [&>svg]:size-5">
          {kindIcons[kind]}
          {entry ? `Edit ${entry.key}` : kindTitles[kind]}
        </DialogTitle>
        <DialogDescription>{kindDescriptions[kind]}</DialogDescription>
      </DialogHeader>
      <div className="space-y-4">
        {entry && (
          <Segmented
            size="sm"
            value={kind}
            onValueChange={(v) => setKind(v as Kind)}
            aria-label="Kind"
          >
            <SegmentedItem value="variable">
              <Variable />
              Variable
            </SegmentedItem>
            <SegmentedItem value="secret">
              <KeyRound />
              Secret
            </SegmentedItem>
            {(ctx.passwordManagers || initialKind === "manager") && (
              <SegmentedItem value="manager">
                <Vault />
                Password manager
              </SegmentedItem>
            )}
          </Segmented>
        )}
        <FloatingInput
          label="Name"
          required
          autoFocus={!entry}
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="API_URL"
          spellCheck={false}
          inputClassName="font-mono"
          aria-invalid={badName || dupName || undefined}
          description={
            badName ? (
              <span className="text-destructive">Letters, digits and _; not starting with a digit.</span>
            ) : dupName ? (
              <span className="text-destructive">Another entry has this name.</span>
            ) : undefined
          }
        />
        {fromProvider ? (
          <ManagerSourceFields
            ms={ms}
            path={path}
            onPathChange={setPath}
            onPickField={(item, field) => !name && setName(fieldEntryName(item, field))}
          />
        ) : (
          <TemplateInput
            value={value}
            onChange={setValue}
            secret={secret}
            placeholder={saved ? "Unchanged" : secret ? "secret value" : "value"}
            sections={sections}
            invalid={unknown.length > 0 || needsValue}
            hint={
              unknown.length > 0 ? (
                <span className="text-destructive">Unknown reference {unknown.join(", ")}.</span>
              ) : needsValue ? (
                <span className="text-destructive">Enter a new value to turn this secret into a variable.</span>
              ) : saved ? (
                "Saved. Leave empty to keep it, or type a new value to replace it."
              ) : sections.length > 0 ? (
                <>
                  Mix text and references, e.g. <Mono>{"test.{{ project.NAME }}"}</Mono>.
                </>
              ) : undefined
            }
          />
        )}
      </div>
      <DialogFooter>
        {onBack && (
          <Button type="button" variant="ghost" onClick={onBack}>
            <ChevronLeft data-icon="inline-start" />
            Back
          </Button>
        )}
        <Button type="submit" disabled={!valid}>
          {entry ? "Done" : kindActions[kind]}
        </Button>
      </DialogFooter>
    </form>
  );
}

const kindIcons: Record<Kind, ReactNode> = { variable: <Variable />, secret: <KeyRound />, manager: <Vault /> };
const kindTitles: Record<Kind, string> = {
  variable: "New variable",
  secret: "New secret",
  manager: "New from password manager",
};
const kindDescriptions: Record<Kind, string> = {
  variable: "Readable by anyone who can see the service.",
  secret: "Write-only once saved: its value reads back masked.",
  manager: "Stays in your vault: fetched on each deploy, never stored by kipitiny.",
};
const kindActions: Record<Kind, string> = { variable: "Add variable", secret: "Add secret", manager: "Add" };
