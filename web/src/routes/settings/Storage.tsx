import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, KeyRound, Lock, Pencil, Plus, Trash2 } from "lucide-react";
import { useState, type FormEvent } from "react";
import { toast } from "sonner";
import { CheckboxField, ChoiceTile, CopyField, ErrorText, Mono, Tag, TestButton } from "@/components/common";
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
import { api, type BackupTarget, type TargetInput } from "@/api";
import {
  storageForm,
  storageIcon,
  storageKind,
  storageLocation,
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
  const encrypt = useMutation({
    meta: { error: "Couldn't encrypt the storage" },
    mutationFn: api.encryptBackupTarget,
    onSuccess: (t) => {
      qc.invalidateQueries({ queryKey: ["storage"] });
      toast.success(`${t.name} is encrypted`, { description: "Copy its key (key icon) somewhere safe, away from this server." });
    },
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
              <TestButton name={t.name} test={() => api.testBackupTarget(t.id)} />
              {t.ageRecipient ? (
                <KeyButton targetId={t.id} name={t.name} />
              ) : (
                <ConfirmDialog
                  trigger={
                    <Button variant="ghost" size="icon-sm" title="Encrypt" loading={encrypt.isPending && encrypt.variables === t.id}>
                      <Lock />
                    </Button>
                  }
                  title={`Encrypt backups on ${t.name}?`}
                  description="An age key is generated for this storage. Backups made from now on are encrypted with it; existing ones stay as they are. It can't be turned off, and without the key (copy it after) encrypted backups are unreadable if this server is lost."
                  confirmLabel="Encrypt"
                  destructive={false}
                  onConfirm={() => encrypt.mutate(t.id)}
                />
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
        <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-lg">
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
            onCheckedChange={(v) => setForm({ ...form, encrypt: v })}
            className="sm:col-span-2"
          />
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
