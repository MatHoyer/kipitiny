import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, FolderSearch, KeyRound, Lock, Pencil, Plus, Trash2 } from "lucide-react";
import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { toast } from "sonner";
import { CheckboxField, ChoiceTile, CopyField, ErrorText, Mono, Tag } from "@/components/common";
import { TestDialog, type PlannedStep } from "@/components/test-dialog";
import { HowTo } from "@/components/how-to";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingSelect } from "@/components/ui/floating-select";
import { api, type BackupTarget, type ScanResult, type TargetInput } from "@/api";
import {
  storageForm,
  storageIcon,
  storageKind,
  storageLocation,
  storageProvider,
  storageProviders,
  type StorageProvider,
} from "@/components/storage";
import { SettingsPage } from "./page";

/** What the dialog shows: the provider picker, a new target, or a target being edited. */
type Editing = { step: "pick" } | { step: "new"; provider: StorageProvider } | { step: "edit"; target: BackupTarget };

/** Where kipitiny stores files: backups today. */
export function Storage() {
  const qc = useQueryClient();
  const targets = useQuery({ queryKey: ["storage"], queryFn: api.backupTargets });
  const [editing, setEditing] = useState<Editing | null>(null);
  const remove = useMutation({
    meta: { error: "Couldn't delete the backup target" },
    mutationFn: api.deleteBackupTarget,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["storage"] }),
  });
  return (
    <SettingsPage
      actions={
        <Button size="sm" onClick={() => setEditing({ step: "pick" })}>
          <Plus data-icon="inline-start" />
          Add target
        </Button>
      }
    >
      <ErrorText error={targets.error} />
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {targets.data?.map((t) => (
          <Card key={t.id} size="sm" className="gap-3 px-4">
            <div className="flex items-start gap-3">
              <span className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-muted [&>svg]:size-5">
                {storageIcon(t)}
              </span>
              <div className="min-w-0 flex-1">
                <p className="truncate font-medium">{t.name}</p>
                <p className="truncate text-xs text-muted-foreground">{storageKind(t)}</p>
              </div>
              {t.ageRecipient && <Tag className="bg-emerald-500/10 text-emerald-600">encrypted</Tag>}
            </div>
            <p className="truncate font-mono text-xs text-muted-foreground" title={storageLocation(t)}>
              {storageLocation(t)}
            </p>
            <div className="mt-auto flex justify-end gap-0.5">
              <TestDialog
                name={t.name}
                icon={storageIcon(t)}
                description="Writes a small file where backups go, then deletes it."
                accent={t.kind === "s3" ? storageProvider(t).accent : undefined}
                plan={storageTestPlan(t)}
                run={(onStep) => api.testBackupTarget(t.id, onStep)}
              />
              <ScanButton target={t} />
              {t.ageRecipient ? (
                <KeyButton targetId={t.id} name={t.name} />
              ) : (
                <EncryptButton target={t} />
              )}
              {t.kind !== "local" && (
                <>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    title="Edit"
                    aria-label="Edit"
                    onClick={() => setEditing({ step: "edit", target: t })}
                  >
                    <Pencil />
                  </Button>
                  <ConfirmDialog
                    trigger={
                      <Button variant="ghost" size="icon-sm" title="Delete" aria-label="Delete" className="text-muted-foreground hover:text-destructive">
                        <Trash2 />
                      </Button>
                    }
                    title={`Delete target ${t.name}?`}
                    description="Backups already stored there are not deleted."
                    onConfirm={() => remove.mutate(t.id)}
                  />
                </>
              )}
            </div>
          </Card>
        ))}
      </div>
      <Dialog open={editing !== null} onOpenChange={(o) => !o && setEditing(null)}>
        <DialogContent className="sm:max-w-lg">
          {editing?.step === "pick" && (
            <>
              <DialogHeader>
                <DialogTitle>Add a target</DialogTitle>
                <DialogDescription>Where should backups go?</DialogDescription>
              </DialogHeader>
              <div className="grid gap-3 sm:grid-cols-2">
                {storageProviders.map((p) => (
                  <ChoiceTile
                    key={p.id}
                    icon={p.icon}
                    title={p.label}
                    description={p.description}
                    onClick={() => setEditing({ step: "new", provider: p })}
                  />
                ))}
              </div>
            </>
          )}
          {editing?.step === "new" && (
            <TargetForm
              key={editing.provider.id}
              provider={editing.provider}
              target={null}
              onBack={() => setEditing({ step: "pick" })}
              onDone={() => setEditing(null)}
            />
          )}
          {editing?.step === "edit" && (
            <TargetForm
              key={editing.target.id}
              {...storageForm(editing.target)}
              target={editing.target}
              onDone={() => setEditing(null)}
            />
          )}
        </DialogContent>
      </Dialog>
    </SettingsPage>
  );
}

/** Adopts the backups another manager left on the storage, and says what was found. */
function ScanButton({ target }: { target: BackupTarget }) {
  const qc = useQueryClient();
  const [result, setResult] = useState<ScanResult | null>(null);
  const scan = useMutation({
    meta: { error: `Couldn't scan ${target.name}` },
    mutationFn: () => api.scanBackupTarget(target.id),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["backups"] });
      setResult(r);
    },
  });
  const n = result?.adopted.length ?? 0;
  return (
    <>
      <Button
        variant="ghost"
        size="icon-sm"
        title="Find backups from another manager"
        aria-label="Find backups from another manager"
        loading={scan.isPending}
        onClick={() => scan.mutate()}
      >
        <FolderSearch />
      </Button>
      <Dialog open={result !== null} onOpenChange={(o) => !o && setResult(null)}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{n === 0 ? `No new backups on ${target.name}` : `${n} backup${n === 1 ? "" : "s"} added`}</DialogTitle>
            <DialogDescription>
              Backups another manager left here, with their record, are added to the history. They belong to no schedule, so
              retention never deletes them.
            </DialogDescription>
          </DialogHeader>
          {result && (
            <ul className="flex flex-col gap-1.5 text-sm">
              {n > 0 && (
                <li>
                  {n - result.orphans} attached to their service here
                  {result.orphans > 0 && (
                    <>
                      , {result.orphans} with no matching service: open one in{" "}
                      <Link className="font-medium hover:underline" to="/backups" onClick={() => setResult(null)}>
                        Backups
                      </Link>{" "}
                      to restore it into a service you pick
                    </>
                  )}
                  .
                </li>
              )}
              {result.known > 0 && <li className="text-muted-foreground">{result.known} already in the history.</li>}
              {result.noRecord > 0 && (
                <li className="text-muted-foreground">
                  {result.noRecord} file{result.noRecord === 1 ? "" : "s"} without a record left alone (backups made before 0.40,
                  manager snapshots).
                </li>
              )}
              {result.unreadable.map((u) => (
                <li key={u.key} className="text-destructive">
                  <Mono>{u.key}</Mono>: {u.error}
                </li>
              ))}
            </ul>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}

function EncryptButton({ target }: { target: BackupTarget }) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [identity, setIdentity] = useState("");
  const encrypt = useMutation({
    meta: { error: "Couldn't encrypt the storage" },
    mutationFn: () => api.encryptBackupTarget(target.id, identity),
    onSuccess: (t) => {
      qc.invalidateQueries({ queryKey: ["storage"] });
      setOpen(false);
      toast.success(`${t.name} is encrypted`, {
        description: identity ? undefined : "Copy its key (key icon) somewhere safe, away from this server.",
      });
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    encrypt.mutate();
  };
  return (
    <>
      <Button variant="ghost" size="icon-sm" title="Encrypt" aria-label="Encrypt" onClick={() => setOpen(true)}>
        <Lock />
      </Button>
      <Dialog
        open={open}
        onOpenChange={(o) => {
          setOpen(o);
          if (!o) setIdentity("");
        }}
      >
        <DialogContent className="sm:max-w-lg">
          <form onSubmit={onSubmit} className="flex flex-col gap-4">
            <DialogHeader>
              <DialogTitle>Encrypt backups on {target.name}?</DialogTitle>
              <DialogDescription>
                Backups made from now on are encrypted with an age key; existing ones stay as they are. It can't be turned off,
                and without the key (copy it after) encrypted backups are unreadable if this server is lost.
              </DialogDescription>
            </DialogHeader>
            <ExistingKeyField value={identity} onChange={setIdentity} />
            <DialogFooter>
              <Button type="submit" loading={encrypt.isPending}>
                Encrypt
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  );
}

/** An age key another manager made, so this one reads the backups it left on the storage. */
function ExistingKeyField({ value, onChange, className }: { value: string; onChange: (v: string) => void; className?: string }) {
  return (
    <FloatingInput
      label="Use an existing key"
      type="password"
      value={value}
      onChange={(e) => onChange(e.target.value)}
      placeholder="AGE-SECRET-KEY-1…"
      autoComplete="off"
      description="Optional: another manager's key, to read the backups it stored here. It must open them. Empty generates a new one."
      className={className}
    />
  );
}

function KeyButton({ targetId, name }: { targetId: string; name: string }) {
  const [key, setKey] = useState<string | null>(null);
  const reveal = useMutation({ meta: { error: "Couldn't show the key" }, mutationFn: () => api.backupTargetKey(targetId), onSuccess: (k) => setKey(k.identity) });
  return (
    <>
      <Button variant="ghost" size="icon-sm" title="Show key" aria-label="Show key" disabled={reveal.isPending} onClick={() => reveal.mutate()}>
        <KeyRound />
      </Button>
      <Dialog open={key !== null} onOpenChange={(o) => !o && setKey(null)}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>Decryption key for {name}</DialogTitle>
            <DialogDescription>Keep it somewhere safe: without it, these backups are unreadable if this server is lost.</DialogDescription>
          </DialogHeader>
          {key && <CopyField value={key} />}
        </DialogContent>
      </Dialog>
    </>
  );
}

const emptyTarget: TargetInput = {
  name: "",
  endpoint: "",
  region: "",
  bucket: "",
  prefix: "",
  accessKey: "",
  secretKey: "",
  useSsl: true,
};

function TargetForm({
  provider: p,
  location: initialLocation = "",
  variant: initialVariant = "",
  target,
  onBack,
  onDone,
}: {
  provider: StorageProvider;
  /** The provider's location (account, region…) of the edited target. */
  location?: string;
  variant?: string;
  target: BackupTarget | null;
  onBack?: () => void;
  onDone: () => void;
}) {
  const qc = useQueryClient();
  const [form, setForm] = useState<TargetInput>(() => {
    if (!target) return { ...emptyTarget, kind: "s3" };
    const { name, endpoint, region, bucket, prefix, accessKey, secretKey, useSsl } = target;
    return { name, endpoint, region, bucket, prefix, accessKey, secretKey, useSsl };
  });
  const loc = p.location;
  const [location, setLocation] = useState(initialLocation || loc?.options?.[0] || "");
  const [variant, setVariant] = useState(initialVariant || p.variant?.options[0].value || "");
  const set = (k: keyof TargetInput) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });
  // A provider's form derives the endpoint and region from the location.
  const input = (): TargetInput =>
    loc ? { ...form, endpoint: loc.endpoint(location.trim(), variant), region: loc.region(location.trim()), useSsl: true } : form;
  const save = useMutation({
    meta: { error: "Couldn't save the backup target" },
    mutationFn: () => (target ? api.updateBackupTarget(target.id, input()) : api.createBackupTarget(input())),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["storage"] });
      onDone();
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };
  return (
    <form onSubmit={onSubmit} className="contents">
      <DialogHeader>
        <DialogTitle className="flex items-center gap-2 [&>svg]:size-5">
          {p.icon}
          {target ? `Edit ${target.name}` : `Connect ${p.label}`}
        </DialogTitle>
        <DialogDescription>{p.help} A test file is written and deleted before saving.</DialogDescription>
      </DialogHeader>
      {!target && p.guide && <HowTo label="How do I set it up?" accent={p.accent} steps={p.guide(form.bucket.trim())} />}
      <div className="grid gap-4 sm:grid-cols-2">
        <FloatingInput label="Name" required autoFocus value={form.name} onChange={set("name")} placeholder="offsite" />
        {loc?.options ? (
          <FloatingSelect
            label={loc.label}
            value={location}
            onValueChange={setLocation}
            options={loc.options.map((o) => ({ value: o, label: o }))}
            description={loc.description}
          />
        ) : loc ? (
          <FloatingInput
            label={loc.label}
            required
            value={location}
            onChange={(e) => setLocation(e.target.value)}
            placeholder={loc.placeholder}
            pattern={loc.pattern}
            description={loc.description}
          />
        ) : (
          <FloatingInput
            label="Endpoint"
            required
            value={form.endpoint}
            onChange={set("endpoint")}
            placeholder="s3.example.com"
            description="host[:port]"
          />
        )}
        {p.variant && (
          <FloatingSelect
            label={p.variant.label}
            value={variant}
            onValueChange={setVariant}
            options={p.variant.options}
            description={p.variant.description}
          />
        )}
        <FloatingInput label="Bucket" required value={form.bucket} onChange={set("bucket")} />
        <FloatingInput
          label="Prefix"
          value={form.prefix}
          onChange={set("prefix")}
          placeholder="kipitiny"
          description="Optional folder inside the bucket."
        />
        <FloatingInput label={p.accessKey} required value={form.accessKey} onChange={set("accessKey")} autoComplete="off" />
        <FloatingInput
          label={p.secretKey}
          required
          type="password"
          value={form.secretKey}
          onChange={set("secretKey")}
          autoComplete="new-password"
          description={target ? "Leave as is to keep the saved one." : undefined}
        />
        {!loc && (
          <>
            <FloatingInput label="Region" value={form.region} onChange={set("region")} description="Usually optional." />
            <CheckboxField
              label="Use HTTPS"
              checked={form.useSsl}
              onCheckedChange={(v) => setForm({ ...form, useSsl: v })}
              className="self-center"
            />
          </>
        )}
        {!target && (
          <CheckboxField
            label={
              <>
                Encrypt backups with <Mono>age</Mono>
              </>
            }
            description="A key is generated for this storage and cannot be changed or removed. Copy it (Show key) somewhere safe: without it, these backups are unreadable if this server is lost."
            checked={form.encrypt ?? false}
            onCheckedChange={(v) => setForm({ ...form, encrypt: v, ageIdentity: "" })}
            className="sm:col-span-2"
          />
        )}
        {!target && form.encrypt && (
          <ExistingKeyField value={form.ageIdentity ?? ""} onChange={(v) => setForm({ ...form, ageIdentity: v })} className="sm:col-span-2" />
        )}
      </div>
      <DialogFooter>
        {onBack && (
          <Button type="button" variant="ghost" className="sm:mr-auto" disabled={save.isPending} onClick={onBack}>
            <ChevronLeft data-icon="inline-start" />
            Back
          </Button>
        )}
        <Button type="submit" loading={save.isPending}>
          {target ? "Save" : "Add target"}
        </Button>
      </DialogFooter>
    </form>
  );
}

/** What testing a target does: the bucket must exist, then a test file is written and deleted. */
function storageTestPlan(t: BackupTarget): PlannedStep[] {
  const write: PlannedStep[] = [
    { id: "write", title: "Write a test file", hint: "A few bytes, next to the backups" },
    { id: "delete", title: "Delete it", hint: "Nothing is left behind" },
  ];
  if (t.kind !== "s3") return write;
  return [{ id: "bucket", title: "Find the bucket", hint: <>With the saved keys, at {t.endpoint}</> }, ...write];
}
