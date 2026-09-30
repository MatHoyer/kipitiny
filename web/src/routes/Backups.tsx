import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { api, type BackupTarget, type TargetInput } from "../api";
import { Button, Card, ErrorText, Field, Input } from "../ui";
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
      <Card title="All backups">
        <ErrorText error={backups.error} />
        <BackupList backups={backups.data ?? []} targets={targets.data ?? []} showService />
      </Card>
    </div>
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
              <p className="font-medium">{t.name}</p>
              <p className="font-mono text-xs text-zinc-500">
                {t.kind === "local" ? "/data/backups on the manager's volume" : `${t.useSsl ? "https" : "http"}://${t.endpoint}/${t.bucket}/${t.prefix}`}
              </p>
            </div>
            {t.kind === "s3" && (
              <div className="space-x-3 text-xs">
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

function TargetForm({ target, onDone }: { target: BackupTarget | null; onDone: () => void }) {
  const qc = useQueryClient();
  const [form, setForm] = useState<TargetInput>(target ?? emptyTarget);
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
