import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { api, type BackupTarget, type TargetInput } from "../api";
import { Button, Card, ErrorText, Field, Input, Select } from "../ui";
import { BackupList } from "./BackupList";

export function Backups() {
  const targets = useQuery({ queryKey: ["backup-targets"], queryFn: api.backupTargets });
  const backups = useQuery({
    queryKey: ["backups", "all"],
    queryFn: () => api.backups(),
    refetchInterval: (q) => (q.state.data?.some((b) => b.status === "running") ? 1_000 : 15_000),
  });

  return (
    <div className="space-y-6">
      <h1 className="text-xl font-semibold">Backups</h1>
      <Targets targets={targets.data ?? []} />
      <ManagerBackup targets={targets.data ?? []} />
      <Card title="All backups">
        <ErrorText error={backups.error} />
        <BackupList backups={backups.data ?? []} targets={targets.data ?? []} showService />
      </Card>
    </div>
  );
}

function ManagerBackup({ targets }: { targets: BackupTarget[] }) {
  const qc = useQueryClient();
  const [targetId, setTargetId] = useState("local");
  const backup = useMutation({
    mutationFn: () => api.backupManager(targetId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["backups"] }),
  });
  return (
    <Card
      title="Manager state"
      actions={
        <>
          <Select value={targetId} onChange={(e) => setTargetId(e.target.value)} className="!w-auto !py-0.5 text-xs">
            {targets.map((t) => (
              <option key={t.id} value={t.id}>
                {t.name}
              </option>
            ))}
          </Select>
          <Button className="!py-0.5 text-xs" disabled={backup.isPending} onClick={() => backup.mutate()}>
            Back up now
          </Button>
        </>
      }
    >
      <p className="text-sm text-zinc-500">
        Projects, services, schedules and backup records live in one SQLite file. It is snapshotted daily to local disk
        by default (<span className="font-mono">KIPITINY_MANAGER_BACKUP_*</span>); send a copy off-site too.
      </p>
      <ErrorText error={backup.error} />
    </Card>
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

function Targets({ targets }: { targets: BackupTarget[] }) {
  const qc = useQueryClient();
  const [editing, setEditing] = useState<BackupTarget | "new" | null>(null);
  const remove = useMutation({
    mutationFn: api.deleteBackupTarget,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["backup-targets"] }),
  });

  return (
    <Card
      title="Targets"
      actions={
        !editing && (
          <Button variant="secondary" className="!py-0.5 text-xs" onClick={() => setEditing("new")}>
            Add S3 target
          </Button>
        )
      }
    >
      <ul className="divide-y divide-zinc-200 text-sm dark:divide-zinc-800">
        {targets.map((t) => (
          <li key={t.id} className="flex items-center justify-between gap-4 py-2">
            <div>
              <p className="font-medium">
                {t.name}
                {t.ageRecipient && <span className="ml-2 text-xs font-normal text-emerald-600">encrypted (age)</span>}
              </p>
              <p className="font-mono text-xs text-zinc-500">
                {t.kind === "local" ? "/data/backups on the manager's volume" : `${t.useSsl ? "https" : "http"}://${t.endpoint}/${t.bucket}/${t.prefix}`}
              </p>
            </div>
            {t.kind === "s3" && (
              <div className="space-x-3 text-xs">
                {t.ageRecipient && <KeyButton targetId={t.id} />}
                <button className="hover:underline" onClick={() => setEditing(t)}>
                  Edit
                </button>
                <button
                  className="text-red-600 hover:underline"
                  onClick={() => confirm(`Delete target ${t.name}?`) && remove.mutate(t.id)}
                >
                  Delete
                </button>
              </div>
            )}
          </li>
        ))}
      </ul>
      <ErrorText error={remove.error} />
      {editing && (
        <TargetForm
          key={editing === "new" ? "new" : editing.id}
          target={editing === "new" ? null : editing}
          onDone={() => setEditing(null)}
        />
      )}
    </Card>
  );
}

function KeyButton({ targetId }: { targetId: string }) {
  const [key, setKey] = useState<string | null>(null);
  const reveal = useMutation({ mutationFn: () => api.backupTargetKey(targetId), onSuccess: (k) => setKey(k.identity) });
  if (key)
    return (
      <span className="inline-flex items-center gap-2">
        <code className="max-w-md rounded bg-zinc-100 px-1 break-all dark:bg-zinc-800">{key}</code>
        <button className="hover:underline" onClick={() => navigator.clipboard.writeText(key)}>
          copy
        </button>
        <button className="hover:underline" onClick={() => setKey(null)}>
          hide
        </button>
      </span>
    );
  return (
    <button className="hover:underline" onClick={() => reveal.mutate()}>
      Show key
    </button>
  );
}

function TargetForm({ target, onDone }: { target: BackupTarget | null; onDone: () => void }) {
  const qc = useQueryClient();
  const [form, setForm] = useState<TargetInput>(() => {
    if (!target) return emptyTarget;
    const { name, endpoint, region, bucket, prefix, accessKey, secretKey, useSsl } = target;
    return { name, endpoint, region, bucket, prefix, accessKey, secretKey, useSsl };
  });
  const set = (k: keyof TargetInput) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });
  const save = useMutation({
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
    <form onSubmit={onSubmit} className="mt-4 grid gap-4 border-t border-zinc-200 pt-4 sm:grid-cols-2 dark:border-zinc-800">
      <Field label="Name">
        <Input required value={form.name} onChange={set("name")} placeholder="offsite" />
      </Field>
      <Field label="Endpoint" hint="host[:port], e.g. s3.eu-west-1.amazonaws.com">
        <Input required value={form.endpoint} onChange={set("endpoint")} />
      </Field>
      <Field label="Bucket">
        <Input required value={form.bucket} onChange={set("bucket")} />
      </Field>
      <Field label="Prefix" hint="Optional folder inside the bucket.">
        <Input value={form.prefix} onChange={set("prefix")} placeholder="kipitiny" />
      </Field>
      <Field label="Access key">
        <Input required value={form.accessKey} onChange={set("accessKey")} autoComplete="off" />
      </Field>
      <Field label="Secret key">
        <Input required type="password" value={form.secretKey} onChange={set("secretKey")} autoComplete="new-password" />
      </Field>
      <Field label="Region" hint="Usually optional.">
        <Input value={form.region} onChange={set("region")} />
      </Field>
      <label className="flex items-center gap-2 self-end pb-2 text-sm">
        <input type="checkbox" checked={form.useSsl} onChange={(e) => setForm({ ...form, useSsl: e.target.checked })} />
        Use HTTPS
      </label>
      {!target && (
        <label className="flex items-start gap-2 text-sm sm:col-span-2">
          <input
            type="checkbox"
            className="mt-1"
            checked={form.encrypt ?? false}
            onChange={(e) => setForm({ ...form, encrypt: e.target.checked })}
          />
          <span>
            Encrypt backups with <span className="font-mono">age</span>
            <span className="block text-xs text-zinc-500">
              A key is generated for this target and cannot be changed later. Copy it (Show key) somewhere safe: without
              it, these backups are unreadable if this server is lost.
            </span>
          </span>
        </label>
      )}
      <div className="flex items-center gap-3 sm:col-span-2">
        <Button disabled={save.isPending}>{save.isPending ? "Testing…" : target ? "Save" : "Add target"}</Button>
        <Button type="button" variant="secondary" onClick={onDone}>
          Cancel
        </Button>
        <span className="text-xs text-zinc-500">A test object is written and deleted before saving.</span>
      </div>
      <ErrorText error={save.error} />
    </form>
  );
}
