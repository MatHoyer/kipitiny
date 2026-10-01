import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, ChevronLeft, Cloud, DatabaseBackup, HardDrive, History, KeyRound, Pencil, Plus, Search, Trash2, TriangleAlert } from "lucide-react";
import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { GoogleDriveIcon, ProtonDriveIcon, ProtonIcon } from "@/components/brand-icons";
import { CheckboxField, ChoiceTile, CopyField, EmptyState, ErrorText, Mono, Section, StatCard, Tag, withCode } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { PageBody, PageHeader } from "@/components/page-header";
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
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useTab } from "@/hooks/use-tab";
import { formatBytes, timeAgo } from "@/lib/format";
import { cn } from "@/lib/utils";
import { api, type Backup, type BackupTarget, type TargetInput, type TargetKind, type TargetKindName } from "../api";
import { BackupList } from "./BackupList";
import { BackupNow } from "./Service";

export function Backups() {
  const targets = useQuery({ queryKey: ["backup-targets"], queryFn: api.backupTargets });
  const backups = useQuery({
    queryKey: ["backups", "all"],
    queryFn: () => api.backups(),
    refetchInterval: (q) => (q.state.data?.some((b) => b.status === "running") ? 1_000 : 15_000),
  });

  const [tab, setTab] = useTab(["backups", "targets", "manager"], "backups");
  const all = backups.data ?? [];

  return (
    <>
      <PageHeader crumbs={[{ label: "Backups" }]} />
      <PageBody>
        <BackupStats backups={backups.data} targets={targets.data?.length} />
        <Tabs value={tab} onValueChange={setTab}>
          <TabsList variant="line">
            <TabsTrigger value="backups">Backups</TabsTrigger>
            <TabsTrigger value="targets">Targets</TabsTrigger>
            <TabsTrigger value="manager">Manager state</TabsTrigger>
          </TabsList>
          <TabsContent value="backups" className="space-y-6">
            <ErrorText error={backups.error} />
            <FilteredBackups backups={all.filter((b) => b.kind === "postgres")} targets={targets.data ?? []} />
          </TabsContent>
          <TabsContent value="targets">
            <Targets targets={targets.data ?? []} />
          </TabsContent>
          <TabsContent value="manager" className="space-y-6">
            <ManagerBackup targets={targets.data ?? []} />
            <BackupList
              backups={all.filter((b) => b.kind === "manager")}
              targets={targets.data ?? []}
              empty={<EmptyState icon={DatabaseBackup} title="No snapshots yet" description="The daily snapshot appears here." />}
            />
          </TabsContent>
        </Tabs>
      </PageBody>
    </>
  );
}

type StatusFilter = "all" | "succeeded" | "failed" | "running";

/** Database backups with search and filters. */
function FilteredBackups({ backups, targets }: { backups: Backup[]; targets: BackupTarget[] }) {
  const [q, setQ] = useState("");
  const [status, setStatus] = useState<StatusFilter>("all");
  const [target, setTarget] = useState("all");
  const needle = q.trim().toLowerCase();
  const rows = backups.filter(
    (b) =>
      (status === "all" || b.status === status) &&
      (target === "all" || b.targetId === target) &&
      (!needle || `${b.projectName} ${b.serviceName}`.toLowerCase().includes(needle)),
  );
  const filtering = needle || status !== "all" || target !== "all";

  return (
    <>
      {backups.length > 0 && (
        <div className="flex flex-wrap items-center gap-2">
          <div className="relative min-w-48 flex-1">
            <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search databases and projects" aria-label="Search" className="pl-9" />
          </div>
          <Select value={status} onValueChange={(v) => setStatus(v as StatusFilter)}>
            <SelectTrigger aria-label="Status" className="min-w-32">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">Any status</SelectItem>
              <SelectItem value="succeeded">Succeeded</SelectItem>
              <SelectItem value="failed">Failed</SelectItem>
              <SelectItem value="running">Running</SelectItem>
            </SelectContent>
          </Select>
          <Select value={target} onValueChange={setTarget}>
            <SelectTrigger aria-label="Target" className="min-w-36">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All targets</SelectItem>
              {targets.map((t) => (
                <SelectItem key={t.id} value={t.id}>
                  {t.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      )}
      <BackupList
        backups={rows}
        targets={targets}
        showService
        empty={
          filtering ? (
            <p className="py-12 text-center text-sm text-muted-foreground">No backup matches these filters.</p>
          ) : undefined
        }
      />
    </>
  );
}

function BackupStats({ backups, targets }: { backups?: Backup[]; targets?: number }) {
  if (!backups) return null;
  const done = backups.filter((b) => b.status === "succeeded");
  const failed = backups.filter((b) => b.status === "failed").length;
  const last = done[0];
  return (
    <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
      <StatCard icon={History} label="Last backup" value={last ? timeAgo(last.createdAt) : "never"} hint={last && (last.serviceName || "manager")} />
      <StatCard icon={HardDrive} label="Stored" value={formatBytes(done.reduce((n, b) => n + b.sizeBytes, 0))} hint={`${done.length} backups`} />
      <StatCard icon={TriangleAlert} label="Failed" value={failed} tone={failed ? "bad" : undefined} />
      <StatCard icon={Cloud} label="Targets" value={targets ?? <Spinner className="size-5 text-muted-foreground" />} />
    </div>
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
      plain
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

/** Brand marks by target kind. */
const kindIcons: Record<TargetKindName, ReactNode> = {
  local: <HardDrive />,
  s3: <Cloud />,
  gdrive: <GoogleDriveIcon className="text-[#4285F4]" />,
  protondrive: <ProtonDriveIcon className="text-[#EB508D]" />,
};

function location(t: BackupTarget) {
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

function Targets({ targets }: { targets: BackupTarget[] }) {
  const qc = useQueryClient();
  const kinds = useQuery({ queryKey: ["backup-target-kinds"], queryFn: api.backupTargetKinds });
  const kindOf = (name: string) => kinds.data?.find((k) => k.kind === name);
  const [editing, setEditing] = useState<Editing | null>(null);
  const remove = useMutation({
    meta: { error: "Couldn't delete the backup target" },
    mutationFn: api.deleteBackupTarget,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["backup-targets"] }),
  });

  return (
    <Section
      plain
      description="Where backups are stored."
      actions={
        <Button size="sm" onClick={() => setEditing({ step: "pick" })}>
          <Plus data-icon="inline-start" />
          Add target
        </Button>
      }
    >
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {targets.map((t) => (
          <Card key={t.id} size="sm" className="gap-3 px-4">
            <div className="flex items-start gap-3">
              <span className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-muted [&>svg]:size-5">
                {kindIcons[t.kind]}
              </span>
              <div className="min-w-0 flex-1">
                <p className="truncate font-medium">{t.name}</p>
                <p className="truncate text-xs text-muted-foreground">{kindOf(t.kind)?.label ?? (t.kind === "local" ? "Built in" : t.kind)}</p>
              </div>
              {t.ageRecipient && <Tag className="bg-emerald-500/10 text-emerald-600">encrypted</Tag>}
            </div>
            <p className="truncate font-mono text-xs text-muted-foreground" title={location(t)}>
              {location(t)}
            </p>
            {t.kind !== "local" && (
              <div className="mt-auto flex justify-end gap-0.5">
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
                    icon={kindIcons[k.kind]}
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
        <DialogTitle className="flex items-center gap-2 [&>svg]:size-5">
          {kindIcons[kind.kind]}
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
