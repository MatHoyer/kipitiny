import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, ChevronLeft, Cloud, HardDrive, KeyRound, Pencil, Plus, Trash2 } from "lucide-react";
import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { GoogleDriveIcon, ProtonDriveIcon, ProtonIcon } from "@/components/brand-icons";
import { CheckboxField, ChoiceTile, CopyField, ErrorText, Mono, Tag, TestButton, withCode } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
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
import { FloatingTextarea } from "@/components/ui/floating-textarea";
import { cn } from "@/lib/utils";
import { api, type BackupTarget, type TargetInput, type TargetKind, type TargetKindName } from "@/api";
import { SettingsPage } from "./page";

/** Brand marks by storage kind. */
const storageIcons: Record<TargetKindName, ReactNode> = {
  local: <HardDrive />,
  s3: <Cloud />,
  gdrive: <GoogleDriveIcon className="text-[#4285F4]" />,
  protondrive: <ProtonDriveIcon className="text-[#EB508D]" />,
};

function storageLocation(t: BackupTarget) {
  const folder = t.prefix ? `/${t.prefix}` : "/";
  switch (t.kind) {
    case "local":
      return "/data/backups on the manager's volume";
    case "s3":
      return `${t.useSsl ? "https" : "http"}://${t.endpoint}/${t.bucket}/${t.prefix}`;
    case "gdrive":
      return `Google Drive ${folder}`;
    case "protondrive":
      return `Proton Drive ${folder}${t.settings?.account ? ` · ${t.settings.account}` : ""}`;
  }
}

/** What the dialog shows: the kind picker, a new target of a kind, or a target being edited. */
type Editing = { step: "pick" } | { step: "new"; kind: TargetKind } | { step: "edit"; target: BackupTarget; kind?: TargetKind };

/** Where kipitiny stores files: backups today. */
export function Storage() {
  const qc = useQueryClient();
  const targets = useQuery({ queryKey: ["storage"], queryFn: api.backupTargets });
  const kinds = useQuery({ queryKey: ["storage-kinds"], queryFn: api.backupTargetKinds });
  const kindOf = (name: string) => kinds.data?.find((k) => k.kind === name);
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
                {storageIcons[t.kind]}
              </span>
              <div className="min-w-0 flex-1">
                <p className="truncate font-medium">{t.name}</p>
                <p className="truncate text-xs text-muted-foreground">{kindOf(t.kind)?.label ?? (t.kind === "local" ? "Built in" : t.kind)}</p>
              </div>
              {t.ageRecipient && <Tag className="bg-emerald-500/10 text-emerald-600">encrypted</Tag>}
            </div>
            <p className="truncate font-mono text-xs text-muted-foreground" title={storageLocation(t)}>
              {storageLocation(t)}
            </p>
            {t.kind !== "local" && (
              <div className="mt-auto flex justify-end gap-0.5">
                <TestButton name={t.name} test={() => api.testBackupTarget(t.id)} />
                {t.ageRecipient && <KeyButton targetId={t.id} name={t.name} />}
                <Button
                  variant="ghost"
                  size="icon-sm"
                  title="Edit"
                  aria-label="Edit"
                  onClick={() => setEditing({ step: "edit", target: t, kind: kindOf(t.kind) })}
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
              </div>
            )}
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
                {kinds.data?.map((k) => (
                  <ChoiceTile
                    key={k.kind}
                    icon={storageIcons[k.kind]}
                    title={k.label}
                    description={k.available ? k.description : "rclone isn't installed on the manager."}
                    disabled={!k.available}
                    onClick={() => setEditing({ step: "new", kind: k })}
                  />
                ))}
              </div>
            </>
          )}
          {editing?.step === "new" && (
            <TargetForm key={editing.kind.kind} kind={editing.kind} target={null} onBack={() => setEditing({ step: "pick" })} onDone={() => setEditing(null)} />
          )}
          {editing?.step === "edit" && editing.kind && (
            <TargetForm key={editing.target.id} kind={editing.kind} target={editing.target} onDone={() => setEditing(null)} />
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
  kind,
  target,
  onBack,
  onDone,
}: {
  kind: TargetKind;
  target: BackupTarget | null;
  onBack?: () => void;
  onDone: () => void;
}) {
  const qc = useQueryClient();
  const isS3 = kind.kind === "s3";
  const [form, setForm] = useState<TargetInput>(() => {
    if (!target) return { ...emptyTarget, prefix: isS3 ? "" : "kipitiny", kind: kind.kind, config: {} };
    const { name, endpoint, region, bucket, prefix, accessKey, secretKey, useSsl } = target;
    return { name, endpoint, region, bucket, prefix, accessKey, secretKey, useSsl, config: { ...target.settings } };
  });
  const set = (k: keyof TargetInput) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });
  const setConfig = (k: string) => (e: { target: { value: string } }) =>
    setForm({ ...form, config: { ...form.config, [k]: e.target.value } });
  const save = useMutation({
    meta: { error: "Couldn't save the backup target" },
    mutationFn: () => (target ? api.updateBackupTarget(target.id, form) : api.createBackupTarget(form)),
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
          {storageIcons[kind.kind]}
          {target ? `Edit ${target.name}` : `New ${kind.label} target`}
        </DialogTitle>
        <DialogDescription>
          {kind.help && <>{withCode(kind.help)} </>}A test file is written and deleted before saving.
        </DialogDescription>
      </DialogHeader>
      <div className="grid gap-4 sm:grid-cols-2">
        <FloatingInput label="Name" required autoFocus value={form.name} onChange={set("name")} placeholder="offsite" />
        {isS3 ? (
          <>
            <FloatingInput
              label="Endpoint"
              required
              value={form.endpoint}
              onChange={set("endpoint")}
              placeholder="s3.eu-west-1.amazonaws.com"
              description="host[:port]"
            />
            <FloatingInput label="Bucket" required value={form.bucket} onChange={set("bucket")} />
            <FloatingInput
              label="Prefix"
              value={form.prefix}
              onChange={set("prefix")}
              placeholder="kipitiny"
              description="Optional folder inside the bucket."
            />
            <FloatingInput label="Access key" required value={form.accessKey} onChange={set("accessKey")} autoComplete="off" />
            <FloatingInput
              label="Secret key"
              required
              type="password"
              value={form.secretKey}
              onChange={set("secretKey")}
              autoComplete="new-password"
            />
            <FloatingInput label="Region" value={form.region} onChange={set("region")} description="Usually optional." />
            <CheckboxField
              label="Use HTTPS"
              checked={form.useSsl}
              onCheckedChange={(v) => setForm({ ...form, useSsl: v })}
              className="self-center"
            />
          </>
        ) : (
          <>
            <FloatingInput
              label="Folder"
              value={form.prefix}
              onChange={set("prefix")}
              placeholder="kipitiny"
              description="Created if missing. Empty for the drive's root."
            />
            {kind.signIn && (
              <ProtonSignIn
                account={target?.settings?.account}
                onSignedIn={(login) => setForm({ ...form, login })}
                className="sm:col-span-2"
              />
            )}
            {kind.fields.map((f) =>
              f.multiline ? (
                <FloatingTextarea
                  key={f.key}
                  label={f.label}
                  required={f.required && !target}
                  value={form.config?.[f.key] ?? ""}
                  onChange={setConfig(f.key)}
                  placeholder={f.placeholder}
                  description={f.description ?? (target && f.secret ? "Leave as is to keep the saved one." : undefined)}
                  className="sm:col-span-2 [&_textarea]:font-mono [&_textarea]:text-xs"
                />
              ) : (
                <FloatingInput
                  key={f.key}
                  label={f.label}
                  type={f.secret ? "password" : "text"}
                  autoComplete={f.secret ? "new-password" : "off"}
                  required={f.required}
                  value={form.config?.[f.key] ?? ""}
                  onChange={setConfig(f.key)}
                  placeholder={f.placeholder}
                  description={f.description}
                />
              ),
            )}
          </>
        )}
        {!target && (
          <CheckboxField
            label={
              <>
                Encrypt backups with <Mono>age</Mono>
              </>
            }
            description="A key is generated for this target and cannot be changed later. Copy it (Show key) somewhere safe: without it, these backups are unreadable if this server is lost."
            checked={form.encrypt ?? false}
            onCheckedChange={(v) => setForm({ ...form, encrypt: v })}
            className="sm:col-span-2"
          />
        )}
      </div>
      <DialogFooter>
        {onBack && (
          <Button type="button" variant="ghost" disabled={save.isPending} onClick={onBack}>
            <ChevronLeft data-icon="inline-start" />
            Back
          </Button>
        )}
        <Button type="submit" loading={save.isPending} disabled={kind.signIn && !target && !form.login}>
          {target ? "Save" : "Add target"}
        </Button>
      </DialogFooter>
    </form>
  );
}

/**
 * Signs in to Proton in the browser, on any device: the manager waits for it,
 * and the target is saved with the session. kipitiny never sees the password.
 */
function ProtonSignIn({
  account,
  onSignedIn,
  className,
}: {
  account?: string;
  onSignedIn: (login: string) => void;
  className?: string;
}) {
  const start = useMutation({ meta: { error: "Couldn't start the Proton sign-in" }, mutationFn: api.startProtonLogin });
  const id = start.data?.id;
  const login = useQuery({
    queryKey: ["proton-login", id],
    queryFn: () => api.protonLogin(id!),
    enabled: !!id,
    refetchInterval: (q) => (q.state.data?.status === "pending" ? 2000 : false),
  });
  const status = login.data?.status ?? (id ? "pending" : undefined);
  useEffect(() => {
    if (id && status === "done") onSignedIn(id);
  }, [id, status]); // once per sign-in

  return (
    <div className={cn("space-y-3 rounded-xl border p-4", className)}>
      {status === "done" ? (
        <p className="flex items-center gap-2 text-sm font-medium text-emerald-700 dark:text-emerald-400">
          <CheckCircle2 className="size-4" />
          Signed in to Proton. Save to use this account.
        </p>
      ) : status === "pending" && start.data ? (
        <>
          <p className="flex items-center gap-2 text-sm font-medium">
            <Spinner />
            Waiting for you to sign in
          </p>
          <p className="text-xs text-muted-foreground">Open this link on any device; it works for 15 minutes.</p>
          <div className="flex flex-wrap gap-2">
            <Button asChild size="sm">
              <a href={start.data.url} target="_blank" rel="noreferrer">
                <ProtonIcon data-icon="inline-start" />
                Open Proton sign-in
              </a>
            </Button>
          </div>
          <CopyField value={start.data.url} />
        </>
      ) : (
        <>
          <p className="text-sm">
            {account ? (
              <>
                Signed in as <span className="font-medium">{account}</span>.
              </>
            ) : (
              "Sign in with the Proton account whose drive receives the backups."
            )}
          </p>
          {status === "failed" && <p className="text-sm text-destructive">{login.data?.error}</p>}
          <Button type="button" size="sm" variant={account ? "outline" : "default"} disabled={start.isPending} onClick={() => start.mutate()}>
            <ProtonIcon data-icon="inline-start" />
            {account || status === "failed" ? "Sign in again" : "Sign in with Proton"}
          </Button>
        </>
      )}
    </div>
  );
}
