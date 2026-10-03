import { useQuery } from "@tanstack/react-query";
import { Braces, ChevronLeft, Eye, EyeOff, FolderSearch, KeyRound, Variable, Vault } from "lucide-react";
import { useRef, useState, type FormEvent, type ReactNode } from "react";
import { Link } from "react-router";
import { PostgresIcon, passwordManagerIcon } from "@/components/brand-icons";
import { ChoiceTile, ErrorText, Mono } from "@/components/common";
import { TemplateValue } from "@/components/template-value";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingSelect } from "@/components/ui/floating-select";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { dbFields, dbRef, envRef, managerRef, refName, refsIn, type EnvReference, type EnvRow } from "@/lib/format";
import { cn } from "@/lib/utils";
import { api, SECRET_MASK } from "../api";

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
  databases?: string[];
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
      <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-lg">
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
                  icon={<PostgresIcon className="text-[#4169E1]" />}
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
  const providers = useQuery({
    queryKey: ["secret-providers"],
    queryFn: api.secretProviders,
    enabled: ctx.passwordManagers || fromProvider,
  });
  const connected = providers.data?.filter((p) => p.connected) ?? [];
  // A saved secret reads back masked: left empty, it keeps its value.
  const saved = !!entry?.secret && entry.value === SECRET_MASK;
  const initialRef = entry ? managerRef(entry.value) : null;
  const scheme = initialRef?.scheme ?? "";
  const [name, setName] = useState(entry?.key ?? "");
  const [value, setValue] = useState(saved || initialRef ? "" : (entry?.value ?? ""));
  const [path, setPath] = useState(initialRef?.path.join("/") ?? "");

  // The schemes on offer: the connected providers, plus the edited entry's
  // own when its provider isn't connected (anymore).
  const sources = [
    ...connected.map((p) => ({ value: p.scheme, label: p.name, icon: passwordManagerIcon(p.id) })),
    ...(scheme && !connected.some((p) => p.scheme === scheme)
      ? [{ value: scheme, label: `${scheme}://`, icon: <KeyRound /> }]
      : []),
  ];
  const [picked, setSource] = useState(scheme);
  const source = picked || sources[0]?.value || "";
  const provider = connected.find((p) => p.scheme === source);

  const key = name.trim();
  const badName = !!key && !envKeyRe.test(key);
  const dupName = taken.includes(key);
  const known = (r: EnvReference) =>
    r.kind === "secret" ||
    (r.kind === "project"
      ? !!ctx.vars?.includes(r.name)
      : !!ctx.databases?.includes(r.db) && (dbFields as readonly string[]).includes(r.field));
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
            label: `Database ${db}`,
            items: dbFields.map((f) => ({ label: f, value: dbRef(db, f) })),
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
          <ToggleGroup
            type="single"
            variant="outline"
            size="sm"
            spacing={0}
            value={kind}
            onValueChange={(v) => v && setKind(v as Kind)}
            aria-label="Kind"
          >
            <ToggleGroupItem value="variable" className="aria-checked:bg-muted">
              <Variable />
              Variable
            </ToggleGroupItem>
            <ToggleGroupItem value="secret" className="aria-checked:bg-muted">
              <KeyRound />
              Secret
            </ToggleGroupItem>
            {(ctx.passwordManagers || initialKind === "manager") && (
              <ToggleGroupItem value="manager" className="aria-checked:bg-muted">
                <Vault />
                Password manager
              </ToggleGroupItem>
            )}
          </ToggleGroup>
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
        {fromProvider && providers.isSuccess && sources.length === 0 && (
          <p className="rounded-lg border border-dashed px-3 py-4 text-sm text-muted-foreground">
            No password manager is connected.{" "}
            <Link to="/password-managers" className="font-medium text-foreground underline-offset-4 hover:underline">
              Connect one in Settings
            </Link>
            .
          </p>
        )}
        {fromProvider && sources.length > 0 && (
          <FloatingSelect
            label="Password manager"
            value={source}
            onValueChange={setSource}
            loading={providers.isLoading}
            options={sources.map((s) => ({
              value: s.value,
              label: (
                <span className="flex items-center gap-2 [&>svg]:size-4 [&>svg]:shrink-0">
                  {s.icon}
                  {s.label}
                </span>
              ),
            }))}
          />
        )}
        {fromProvider ? (
          source && (
            <ProviderPath
              scheme={source}
              providerId={provider?.id}
              path={path}
              onChange={setPath}
              onPickField={(item, field) => !name && setName(fieldEntryName(item, field))}
            />
          )
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

type RefSection = { label: string; items: { label: string; value: string }[] };

/**
 * A value input that inserts references at the cursor, previewing them as
 * chips. A secret is typed blind unless revealed.
 */
function TemplateInput({
  value,
  onChange,
  secret,
  placeholder,
  sections,
  invalid,
  hint,
}: {
  value: string;
  onChange: (v: string) => void;
  secret: boolean;
  placeholder: string;
  sections: RefSection[];
  invalid: boolean;
  hint?: ReactNode;
}) {
  const input = useRef<HTMLInputElement>(null);
  const [shown, setShown] = useState(false);
  const hidden = secret && !shown;
  const insert = (ref: string) => {
    const el = input.current;
    const start = el?.selectionStart ?? value.length;
    const end = el?.selectionEnd ?? value.length;
    onChange(value.slice(0, start) + ref + value.slice(end));
    requestAnimationFrame(() => {
      el?.focus();
      el?.setSelectionRange(start + ref.length, start + ref.length);
    });
  };
  const hasRefs = refsIn(value).length > 0;

  return (
    <div className="space-y-1.5">
      <div className="flex items-start gap-2">
        <div className="relative min-w-0 flex-1">
          <FloatingInput
            ref={input}
            label="Value"
            type={hidden ? "password" : "text"}
            autoComplete={secret ? "new-password" : "off"}
            value={value}
            onChange={(e) => onChange(e.target.value)}
            placeholder={placeholder}
            spellCheck={false}
            aria-invalid={invalid || undefined}
            inputClassName={cn("font-mono text-sm", secret && "pr-12")}
          />
          {secret && (
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label={shown ? "Hide value" : "Show value"}
              title={shown ? "Hide value" : "Show value"}
              onClick={() => setShown(!shown)}
              className="absolute top-1/2 right-2 -translate-y-1/2"
            >
              {shown ? <EyeOff /> : <Eye />}
            </Button>
          )}
        </div>
        {sections.length > 0 && (
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button type="button" variant="outline" size="icon" className="mt-3 shrink-0" title="Insert a reference" aria-label="Insert a reference">
                <Braces />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="max-h-80 min-w-48">
              {sections.map((s, n) => (
                <div key={s.label}>
                  {n > 0 && <DropdownMenuSeparator />}
                  <DropdownMenuLabel className="text-xs">{s.label}</DropdownMenuLabel>
                  {s.items.map((item) => (
                    <DropdownMenuItem key={item.value} onSelect={() => insert(item.value)} className="font-mono text-xs">
                      {item.label}
                    </DropdownMenuItem>
                  ))}
                </div>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        )}
      </div>
      {hasRefs && (
        <div className="flex min-h-8 items-center gap-2 rounded-lg bg-muted/50 px-2 py-1 text-xs">
          <span className="shrink-0 text-muted-foreground">Preview</span>
          <TemplateValue value={value} maskText={hidden} />
        </div>
      )}
      {hint && <p className="px-1 text-xs text-muted-foreground">{hint}</p>}
    </div>
  );
}

/**
 * The path of a password manager reference, typed after its scheme or picked
 * by browsing the provider's vaults.
 */
function ProviderPath({
  scheme,
  providerId,
  path,
  onChange,
  onPickField,
}: {
  scheme: string;
  /** Unset when the scheme's provider isn't connected: no browsing. */
  providerId?: string;
  path: string;
  onChange: (path: string) => void;
  onPickField: (item: string, field: string) => void;
}) {
  const [browsing, setBrowsing] = useState(false);
  const [vault, setVault] = useState("");
  const [item, setItem] = useState("");
  const vaults = useQuery({
    queryKey: ["secret-vaults", providerId],
    queryFn: () => api.secretVaults(providerId!),
    enabled: browsing && !!providerId,
  });
  const items = useQuery({
    queryKey: ["secret-items", providerId, vault],
    queryFn: () => api.secretItems(providerId!, vault),
    enabled: browsing && !!providerId && !!vault,
  });
  // Titles needn't be unique: items are picked by position.
  const picked = item === "" ? undefined : items.data?.[Number(item)];
  const prefix = `${scheme}://`;
  const fieldRef = `${prefix}${path}`;

  return (
    <div className="space-y-3">
      <div className="flex items-start gap-2">
        <span className="flex h-14 shrink-0 items-center rounded-xl border bg-muted px-3 font-mono text-sm text-muted-foreground">
          {prefix}
        </span>
        <FloatingInput
          label="Reference"
          required
          value={path}
          // A pasted full reference drops its scheme.
          onChange={(e) => onChange(e.target.value.startsWith(prefix) ? e.target.value.slice(prefix.length) : e.target.value)}
          placeholder="Vault/Item/field"
          spellCheck={false}
          className="min-w-0 flex-1"
          inputClassName="font-mono text-sm"
        />
        {providerId && (
          <Button
            type="button"
            variant={browsing ? "secondary" : "outline"}
            size="icon"
            className="mt-3 shrink-0"
            title="Browse"
            aria-label="Browse"
            aria-pressed={browsing}
            onClick={() => setBrowsing(!browsing)}
          >
            <FolderSearch />
          </Button>
        )}
      </div>
      {browsing && providerId && (
        <div className="space-y-3 rounded-xl border border-dashed p-3">
          <FloatingSelect
            label="Vault"
            loading={vaults.isLoading}
            value={vault}
            onValueChange={(v) => {
              setVault(v);
              setItem("");
            }}
            options={(vaults.data ?? []).map((v) => ({ value: v, label: v }))}
            disabled={!vaults.data?.length}
            description="Only vaults the token can read are listed."
          />
          <FloatingSelect
            label="Item"
            loading={items.isLoading}
            value={item}
            onValueChange={setItem}
            options={(items.data ?? []).map((i, n) => ({ value: String(n), label: i.title }))}
            disabled={!items.data?.length}
          />
          <FloatingSelect
            label="Field"
            value={picked?.fields.some((f) => f.ref === fieldRef) ? fieldRef : ""}
            onValueChange={(ref) => {
              const f = picked?.fields.find((f) => f.ref === ref);
              if (!f || !picked) return;
              onChange(ref.startsWith(prefix) ? ref.slice(prefix.length) : ref);
              onPickField(picked.title, f.name);
            }}
            options={(picked?.fields ?? []).map((f) => ({ value: f.ref, label: f.name }))}
            disabled={!picked?.fields.length}
          />
          <ErrorText error={vaults.error ?? items.error} />
        </div>
      )}
    </div>
  );
}

/** The two ways to connect a database. */
const dbShapes = {
  url: { label: "DATABASE_URL", fields: [["DATABASE_URL", "URL"]] },
  split: {
    label: "PGHOST, PGPORT, PGUSER…",
    fields: dbFields.filter((f) => f !== "URL").map((f) => [`PG${f}`, f]),
  },
} satisfies Record<string, { label: string; fields: string[][] }>;

/** Adds a database's credentials as secrets referencing it. */
function DatabaseForm({
  databases,
  taken,
  onBack,
  onDone,
}: {
  databases: string[];
  taken: string[];
  onBack: () => void;
  onDone: (rows: EnvRow[]) => void;
}) {
  const [db, setDb] = useState(databases[0]);
  const [shape, setShape] = useState<keyof typeof dbShapes>("url");
  // The usual name, or prefixed by the database when taken (several
  // databases, one service).
  const entryName = (key: string) => (taken.includes(key) ? `${db.toUpperCase().replaceAll("-", "_")}_${key}` : key);
  const rows = dbShapes[shape].fields.map(([key, field]) => ({
    key: entryName(key),
    value: dbRef(db, field),
    secret: true,
  }));
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    e.stopPropagation();
    onDone(rows);
  };

  return (
    <form onSubmit={onSubmit} className="contents">
      <DialogHeader>
        <DialogTitle className="flex items-center gap-2 [&>svg]:size-5">
          <PostgresIcon className="text-[#4169E1]" />
          Connect a database
        </DialogTitle>
        <DialogDescription>Added as secrets that follow the database&apos;s credentials.</DialogDescription>
      </DialogHeader>
      <div className="space-y-4">
        {databases.length > 1 && (
          <FloatingSelect
            label="Database"
            value={db}
            onValueChange={setDb}
            options={databases.map((d) => ({ value: d, label: d }))}
          />
        )}
        <ToggleGroup
          type="single"
          variant="outline"
          spacing={0}
          value={shape}
          onValueChange={(v) => v && setShape(v as keyof typeof dbShapes)}
          aria-label="Variables"
          className="w-full"
        >
          {Object.entries(dbShapes).map(([k, s]) => (
            <ToggleGroupItem key={k} value={k} className="flex-1 font-mono text-xs aria-checked:bg-muted">
              {s.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
        <ul className="space-y-1 rounded-xl border p-3">
          {rows.map((r) => (
            <li key={r.key} className="flex items-center gap-3 text-sm">
              <span className="w-2/5 shrink-0 truncate font-mono">{r.key}</span>
              <TemplateValue value={r.value} />
            </li>
          ))}
        </ul>
      </div>
      <DialogFooter>
        <Button type="button" variant="ghost" onClick={onBack}>
          <ChevronLeft data-icon="inline-start" />
          Back
        </Button>
        <Button type="submit">Connect</Button>
      </DialogFooter>
    </form>
  );
}
