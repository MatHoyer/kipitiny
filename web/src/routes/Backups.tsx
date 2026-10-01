import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound, Pencil, Plus, Trash2 } from "lucide-react";
import { useState, type FormEvent } from "react";
import { CheckboxField, CopyField, ErrorText, Mono, Section, Tag } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { PageBody, PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { api, type BackupTarget, type TargetInput } from "../api";
import { BackupList } from "./BackupList";
import { BackupNow } from "./Service";

export function Backups() {
  const targets = useQuery({ queryKey: ["backup-targets"], queryFn: api.backupTargets });
  const backups = useQuery({
    queryKey: ["backups", "all"],
    queryFn: () => api.backups(),
    refetchInterval: (q) => (q.state.data?.some((b) => b.status === "running") ? 1_000 : 15_000),
  });

  return (
    <>
      <PageHeader crumbs={[{ label: "Backups" }]} />
      <PageBody>
        <Targets targets={targets.data ?? []} />
        <ManagerBackup targets={targets.data ?? []} />
        <Section title="All backups">
          <ErrorText error={backups.error} />
          <BackupList backups={backups.data ?? []} targets={targets.data ?? []} showService />
        </Section>
      </PageBody>
    </>
  );
}

function ManagerBackup({ targets }: { targets: BackupTarget[] }) {
  const qc = useQueryClient();
  const [targetId, setTargetId] = useState("local");
  const backup = useMutation({
    meta: { error: "Couldn't back up the manager" },
    mutationFn: () => api.backupManager(targetId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["backups"] }),
  });
  return (
    <Section
      title="Manager state"
      description={
        <>
          Projects, services, schedules and backup records live in one SQLite file. It is snapshotted daily to local disk
          by default (<Mono>KIPITINY_MANAGER_BACKUP_*</Mono>); send a copy off-site too.
        </>
      }
      actions={<BackupNow targets={targets} targetId={targetId} onTarget={setTargetId} disabled={backup.isPending} onBackup={() => backup.mutate()} />}
    />
  );
}

function Targets({ targets }: { targets: BackupTarget[] }) {
  const qc = useQueryClient();
  const [editing, setEditing] = useState<BackupTarget | "new" | null>(null);
  const remove = useMutation({
    meta: { error: "Couldn't delete the backup target" },
    mutationFn: api.deleteBackupTarget,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["backup-targets"] }),
  });

  return (
    <Section
      title="Targets"
      description="Where backups are stored."
      actions={
        <Button variant="outline" size="sm" onClick={() => setEditing("new")}>
          <Plus data-icon="inline-start" />
          Add S3 target
        </Button>
      }
    >
      <ul className="-my-2 divide-y">
        {targets.map((t) => (
          <li key={t.id} className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 py-2.5">
            <div className="min-w-0">
              <p className="flex items-center gap-2 text-sm font-medium">
                {t.name}
                {t.ageRecipient && <Tag className="bg-emerald-500/10 text-emerald-600">encrypted (age)</Tag>}
              </p>
              <p className="truncate font-mono text-xs text-muted-foreground">
                {t.kind === "local"
                  ? "/data/backups on the manager's volume"
                  : `${t.useSsl ? "https" : "http"}://${t.endpoint}/${t.bucket}/${t.prefix}`}
              </p>
            </div>
            {t.kind === "s3" && (
              <div className="flex gap-0.5">
                {t.ageRecipient && <KeyButton targetId={t.id} name={t.name} />}
                <Button variant="ghost" size="icon-sm" title="Edit" aria-label="Edit" onClick={() => setEditing(t)}>
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
          </li>
        ))}
      </ul>
      <Dialog open={editing !== null} onOpenChange={(o) => !o && setEditing(null)}>
        <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-lg">
          {editing && (
            <TargetForm
              key={editing === "new" ? "new" : editing.id}
              target={editing === "new" ? null : editing}
              onDone={() => setEditing(null)}
            />
          )}
        </DialogContent>
      </Dialog>
    </Section>
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

function TargetForm({ target, onDone }: { target: BackupTarget | null; onDone: () => void }) {
  const qc = useQueryClient();
  const [form, setForm] = useState<TargetInput>(() => {
    if (!target) return emptyTarget;
    const { name, endpoint, region, bucket, prefix, accessKey, secretKey, useSsl } = target;
    return { name, endpoint, region, bucket, prefix, accessKey, secretKey, useSsl };
  });
  const set = (k: keyof TargetInput) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });
  const save = useMutation({
    meta: { error: "Couldn't save the backup target" },
    mutationFn: () => (target ? api.updateBackupTarget(target.id, form) : api.createBackupTarget(form)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["backup-targets"] });
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
        <DialogTitle>{target ? `Edit ${target.name}` : "New S3 target"}</DialogTitle>
        <DialogDescription>A test object is written and deleted before saving.</DialogDescription>
      </DialogHeader>
      <div className="grid gap-4 sm:grid-cols-2">
        <FloatingInput label="Name" required autoFocus value={form.name} onChange={set("name")} placeholder="offsite" />
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
        <Button type="submit" disabled={save.isPending}>
          {save.isPending ? "Testing…" : target ? "Save" : "Add target"}
        </Button>
      </DialogFooter>
    </form>
  );
}
