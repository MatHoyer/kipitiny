import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArchiveRestore, Clock, DatabaseBackup, Download, HardDrive, ShieldCheck, Trash2 } from "lucide-react";
import { useState, type FormEvent, type ReactNode } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { toast } from "sonner";
import { CopyButton, DangerZone, ErrorText, Mono, Section, StatCard, StateBadge, Tag, Loading } from "@/components/common";
import { databaseLabels } from "@/components/brand-icons";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { PageBody, PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingSelect } from "@/components/ui/floating-select";
import { Spinner } from "@/components/ui/spinner";
import { BackupSteps, RestoreSteps, VerifySteps, useLastStep } from "@/components/op-steps";
import { formatBytes, formatDateTime, formatDuration, timeAgo } from "@/lib/format";
import { api, type Backup as BackupT, type DatabaseKind, type Restore, type Service } from "../api";
import { BackupIcon, StorageName, backupTitle } from "./BackupList";

/** One backup: what it holds, where it is, whether it restores, and its restores. */
export function Backup() {
  const { id = "" } = useParams();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const backup = useQuery({
    queryKey: ["backups", "one", id],
    queryFn: () => api.getBackup(id),
    refetchInterval: (q) => (q.state.data?.status === "running" || q.state.data?.verifyStatus === "running" ? 1_000 : 15_000),
  });
  const targets = useQuery({ queryKey: ["storage"], queryFn: api.backupTargets });
  const b = backup.data;
  // A service's backup (not the manager's). The service may be gone:
  // backups outlive it.
  const isService = !!b && b.kind !== "manager";
  const service = useQuery({
    queryKey: ["service", b?.serviceId],
    queryFn: () => api.service(b!.serviceId),
    // Adopted from another manager without a matching service: none.
    enabled: isService && !!b.serviceId,
    retry: false,
  });
  const restores = useQuery({
    queryKey: ["restores", b?.serviceId],
    queryFn: () => api.restores(b!.serviceId),
    enabled: isService && !!service.data,
    refetchInterval: (q) => (q.state.data?.some((r) => r.status === "running") ? 1_000 : 15_000),
  });

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["backups"] });
    qc.invalidateQueries({ queryKey: ["restores"] });
  };
  const verify = useMutation({ meta: { error: "Couldn't start the restore test" }, mutationFn: () => api.verifyBackup(id), onSuccess: invalidate });
  const restore = useMutation({
    meta: { error: "Couldn't restore the backup" },
    mutationFn: ({ confirm, serviceId }: { confirm: string; serviceId?: string }) => api.restore(id, confirm, serviceId),
    onSuccess: () => {
      invalidate();
      toast.success("Restore started");
    },
  });
  const remove = useMutation({
    meta: { error: "Couldn't delete the backup" },
    mutationFn: () => api.deleteBackup(id),
    onSuccess: () => {
      invalidate();
      navigate("/backups");
    },
  });

  const crumbs = [{ label: "Backups", to: "/backups" }, { label: b ? backupTitle(b) : <Spinner className="size-3.5" /> }];
  if (!b)
    return (
      <>
        <PageHeader crumbs={crumbs} />
        <PageBody>{backup.error ? <ErrorText error={backup.error} /> : <Loading />}</PageBody>
      </>
    );

  const target = targets.data?.find((t) => t.id === b.targetId);
  const done = b.status === "succeeded";
  const ownRestores = restores.data?.filter((r) => r.backupId === b.id) ?? [];
  const restoring = restores.data?.some((r) => r.status === "running");

  return (
    <>
      <PageHeader
        crumbs={crumbs}
        actions={
          done && (
            <>
              <Button asChild variant="outline" size="sm">
                <a href={api.downloadUrl(b.id)}>
                  <Download data-icon="inline-start" />
                  Download
                </a>
              </Button>
              {isService && service.data && (
                <ConfirmDialog
                  trigger={
                    <Button size="sm" disabled={restore.isPending || restoring}>
                      <ArchiveRestore data-icon="inline-start" />
                      Restore
                    </Button>
                  }
                  title={`Restore into ${b.serviceName}?`}
                  description={
                    b.kind === "volume"
                      ? "The content of its volumes is replaced by this backup; the service is stopped meanwhile."
                      : "Its current data is replaced by this backup; apps using the database are stopped meanwhile."
                  }
                  confirmLabel="Restore"
                  typeToConfirm={b.serviceName}
                  onConfirm={(name) => restore.mutate({ confirm: name })}
                />
              )}
              {isService && !service.data && !service.isLoading && (
                <RestoreInto backup={b} pending={restore.isPending} onRestore={(serviceId, confirm) => restore.mutate({ confirm, serviceId })} />
              )}
            </>
          )
        }
      />
      <PageBody>
        <div className="flex flex-wrap items-center gap-4">
          <span className="flex size-12 items-center justify-center rounded-xl bg-muted">
            <BackupIcon backup={b} className="size-6" />
          </span>
          <div className="min-w-0 flex-1">
            <h2 className="flex flex-wrap items-center gap-2 text-xl font-semibold">
              {backupTitle(b)}
              <StateBadge state={b.status} />
              {b.encrypted && <Tag>encrypted (age)</Tag>}
            </h2>
            <p className="text-sm text-muted-foreground" title={formatDateTime(b.createdAt)}>
              {new Date(b.createdAt).toLocaleString([], { dateStyle: "full", timeStyle: "short" })}
              {timeAgo(b.createdAt).endsWith("ago") && <span className="opacity-70"> ({timeAgo(b.createdAt)})</span>}
            </p>
          </div>
        </div>
        {b.error && (
          <p role="alert" className="rounded-xl border border-destructive/30 bg-destructive/5 p-4 text-sm text-destructive">
            {b.error}
          </p>
        )}

        <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
          <StatCard icon={HardDrive} label="Size" value={done ? formatBytes(b.sizeBytes) : "—"} />
          <StatCard icon={Clock} label="Took" value={b.finishedAt ? formatDuration(b.durationMs) : <Spinner className="size-5 text-muted-foreground" />} />
          {b.kind === "postgres" || b.kind === "dump" ? (
            <StatCard icon={DatabaseBackup} label={databaseLabels[b.serviceKind as DatabaseKind] ?? "PostgreSQL"} value={b.pgVersion || "—"} />
          ) : b.kind === "volume" ? (
            <StatCard icon={HardDrive} label={b.volumes?.length === 1 ? "Volume" : "Volumes"} value={b.volumes?.join(", ") || "—"} />
          ) : (
            <StatCard icon={DatabaseBackup} label="Kind" value="SQLite" />
          )}
          <StatCard
            icon={ShieldCheck}
            label="Restore test"
            value={b.verifyStatus === "succeeded" ? "passed" : b.verifyStatus === "failed" ? "failed" : b.verifyStatus === "running" ? "running" : "—"}
            tone={b.verifyStatus === "succeeded" ? "good" : b.verifyStatus === "failed" ? "bad" : undefined}
            hint={b.verifiedAt && timeAgo(b.verifiedAt)}
          />
        </div>

        <Section title="Details">
          <dl className="grid gap-x-6 gap-y-4 text-sm sm:grid-cols-2">
            {isService && (
              <Detail label="Service">
                {service.data ? (
                  <Link className="font-medium hover:underline" to={`/services/${b.serviceId}`}>
                    {b.serviceName}
                  </Link>
                ) : (
                  <>
                    {b.serviceName} <span className="text-muted-foreground">(deleted)</span>
                  </>
                )}
              </Detail>
            )}
            {isService && (
              <Detail label="Project">
                <Link className="hover:underline" to={`/projects/${b.projectId}`}>
                  {b.projectName}
                </Link>
              </Detail>
            )}
            <Detail label="Target">
              <Link className="hover:underline" to="/storage">
                <StorageName target={target} className="[&>svg]:size-3.5" />
              </Link>
            </Detail>
            {b.image && (
              <Detail label="Image">
                <Mono>{b.image}</Mono>
              </Detail>
            )}
            {!!b.extensions?.length && <Detail label="Extensions">{b.extensions.join(", ")}</Detail>}
            <Detail label="Encryption">{b.encrypted ? "age (X25519), with the target's key" : "none"}</Detail>
            <Detail label="Started">{formatDateTime(b.createdAt)}</Detail>
            <Detail label="Finished">{b.finishedAt ? formatDateTime(b.finishedAt) : "—"}</Detail>
            <Detail label="Object" className="sm:col-span-2">
              <CopyValue value={b.objectKey} />
            </Detail>
            {b.sha256 && (
              <Detail label="SHA-256" className="sm:col-span-2">
                <CopyValue value={b.sha256} />
              </Detail>
            )}
          </dl>
        </Section>

        <BackupProgress backup={b} />

        {isService && done && <RestoreTest backup={b} onRun={() => verify.mutate()} busy={verify.isPending} />}

        {isService && service.data && (
          <Section title="Restores" description={ownRestores.length === 0 ? "This backup was never restored." : undefined}>
            {ownRestores.length > 0 && (
              <ul className="-my-2 divide-y">
                {ownRestores.map((r) => (
                  <RestoreRow key={r.id} restore={r} backup={b} />
                ))}
              </ul>
            )}
          </Section>
        )}

        {b.status !== "running" && (
          <DangerZone description="Deleting removes the backup from its target for good.">
            <ConfirmDialog
              trigger={
                <Button variant="destructive" size="sm" disabled={remove.isPending}>
                  <Trash2 data-icon="inline-start" />
                  Delete backup
                </Button>
              }
              title="Delete this backup?"
              description="It is removed from its target permanently."
              onConfirm={() => remove.mutate()}
            />
          </DangerZone>
        )}
      </PageBody>
    </>
  );
}

/** The latest restore test: a throwaway server of the database's kind loads
 * the dump, or a throwaway container reads the whole volume archive. */
function RestoreTest({ backup: b, onRun, busy }: { backup: BackupT; onRun: () => void; busy: boolean }) {
  const running = b.verifyStatus === "running";
  const seen = useLastStep(running, b.verifyStep);
  const volume = b.kind === "volume";
  const server = databaseLabels[b.serviceKind as DatabaseKind] ?? "PostgreSQL";
  return (
    <Section
      title="Restore test"
      description={
        volume
          ? "Decrypts, decompresses and lists the whole archive in a throwaway container with no network, and checks its checksum."
          : `Loads the backup into a throwaway ${server} with no network, then measures what came back.`
      }
      actions={
        <Button variant="outline" size="sm" loading={running} disabled={busy} onClick={onRun}>
          <ShieldCheck data-icon="inline-start" />
          {running ? "Testing" : b.verifyStatus ? "Test again" : "Run test"}
        </Button>
      }
    >
      {!b.verifyStatus ? (
        <p className="text-sm text-muted-foreground">Not tested yet. A backup nobody restored is a hope.</p>
      ) : running || seen ? (
        // Seen running here: its steps stay, with the outcome under them.
        <div className="grid gap-4">
          <VerifySteps backup={b} server={server} step={seen?.step} />
          {b.verifyStatus === "failed" && <p className="text-sm text-destructive animate-in fade-in">{b.verifyError}</p>}
          {b.verifyStatus === "succeeded" && <VerifyDetails backup={b} />}
        </div>
      ) : b.verifyStatus === "failed" ? (
        <p className="text-sm text-destructive">{b.verifyError}</p>
      ) : (
        <VerifyDetails backup={b} />
      )}
    </Section>
  );
}

/** What a passed restore test measured. */
function VerifyDetails({ backup: b }: { backup: BackupT }) {
  const d = b.verifyDetails;
  const volume = b.kind === "volume";
  // Dumps other than PostgreSQL's count rows exactly; MongoDB holds collections of documents.
  const mongo = b.serviceKind === "mongodb";
  return volume ? (
    <dl className="grid grid-cols-2 gap-4 text-sm md:grid-cols-4">
      <Detail label="Files and folders">{(d.files ?? 0).toLocaleString()}</Detail>
      <Detail label="Read in">{formatDuration(d.durationMs)}</Detail>
      {b.verifiedAt && <p className="col-span-full text-xs text-muted-foreground">Tested {formatDateTime(b.verifiedAt)}</p>}
    </dl>
  ) : (
    <dl className="grid grid-cols-2 gap-4 text-sm md:grid-cols-4">
      <Detail label={mongo ? "Collections" : "Tables"}>{d.tables}</Detail>
      <Detail label={mongo ? "Documents" : b.kind === "dump" ? "Rows" : "Rows (estimated)"}>{d.rows.toLocaleString()}</Detail>
      <Detail label="Database size">{formatBytes(d.dbBytes)}</Detail>
      <Detail label="Restored in">{formatDuration(d.durationMs)}</Detail>
      {b.verifiedAt && <p className="col-span-full text-xs text-muted-foreground">Tested {formatDateTime(b.verifiedAt)}</p>}
    </dl>
  );
}

/** A running backup's steps, kept with the outcome once it ends here. */
function BackupProgress({ backup: b }: { backup: BackupT }) {
  const running = b.status === "running";
  const seen = useLastStep(running, b.step);
  if (!running && !seen) return null;
  return (
    <Section
      title={running ? "In progress" : b.status === "succeeded" ? "Done" : "Failed"}
      description={running ? "Nothing is kept unless it finishes: a failed backup is deleted from its target." : undefined}
    >
      <div className="grid gap-3">
        <BackupSteps backup={b} step={seen?.step} />
        {b.status === "failed" && <p className="text-sm text-destructive animate-in fade-in">{b.error}</p>}
      </div>
    </Section>
  );
}

/** A restore of this backup; one running here keeps its steps once it ends. */
function RestoreRow({ restore: r, backup: b }: { restore: Restore; backup: BackupT }) {
  const seen = useLastStep(r.status === "running", r.step);
  return (
    <li className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2.5 text-sm">
      <StateBadge state={r.status} />
      <span title={formatDateTime(r.createdAt)}>{timeAgo(r.createdAt)}</span>
      {r.finishedAt && (
        <span className="text-muted-foreground">took {formatDuration(new Date(r.finishedAt).getTime() - new Date(r.createdAt).getTime())}</span>
      )}
      {r.error && <span className="w-full text-xs text-destructive">{r.error}</span>}
      {(r.status === "running" || seen) && (
        <div className="w-full pt-2">
          <RestoreSteps restore={r} backup={b} step={seen?.step} />
        </div>
      )}
    </li>
  );
}

function Detail({ label, children, className }: { label: string; children: ReactNode; className?: string }) {
  return (
    <div className={className}>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-0.5 min-w-0">{children}</dd>
    </div>
  );
}

function CopyValue({ value }: { value: string }) {
  return (
    <span className="flex items-start gap-1">
      <Mono>
        <span className="break-all">{value}</span>
      </Mono>
      <CopyButton value={value} />
    </span>
  );
}


/** Whether a backup restores into svc; the server checks volumes and the rest. */
function fits(b: BackupT, svc: Service) {
  if (b.kind === "postgres") return svc.kind === "postgres";
  if (b.kind === "dump") return svc.kind === b.serviceKind;
  return svc.kind === "app" || svc.kind === "redis";
}

/** Restores a backup whose service is gone (deleted, or adopted from another manager) into one picked by hand. */
function RestoreInto({ backup: b, pending, onRestore }: { backup: BackupT; pending: boolean; onRestore: (serviceId: string, confirm: string) => void }) {
  const [open, setOpen] = useState(false);
  const [projectId, setProjectId] = useState("");
  const [serviceId, setServiceId] = useState("");
  const [typed, setTyped] = useState("");
  const projects = useQuery({ queryKey: ["projects"], queryFn: api.projects, enabled: open });
  const services = useQuery({ queryKey: ["services", projectId], queryFn: () => api.services(projectId), enabled: open && !!projectId });
  const options = services.data?.filter((s) => fits(b, s)) ?? [];
  const svc = options.find((s) => s.id === serviceId);
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (!svc || typed !== svc.name) return;
    onRestore(svc.id, typed);
    setOpen(false);
  };
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (!o) setTyped("");
      }}
    >
      <DialogTrigger asChild>
        <Button size="sm" disabled={pending}>
          <ArchiveRestore data-icon="inline-start" />
          Restore into…
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={onSubmit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>Restore into another service</DialogTitle>
            <DialogDescription>
              {b.projectName}/{b.serviceName} isn't here. Its data replaces the picked service's;
              {b.kind === "volume" ? " the service is stopped meanwhile." : " apps using the database are stopped meanwhile."}
            </DialogDescription>
          </DialogHeader>
          <FloatingSelect
            label="Project"
            value={projectId}
            loading={projects.isLoading}
            onValueChange={(v) => {
              setProjectId(v);
              setServiceId("");
            }}
            options={projects.data?.map((p) => ({ value: p.id, label: p.name })) ?? []}
          />
          <FloatingSelect
            label="Service"
            value={serviceId}
            disabled={!projectId}
            loading={services.isLoading}
            onValueChange={setServiceId}
            options={options.map((s) => ({ value: s.id, label: s.name }))}
            description={projectId && services.data && options.length === 0 ? "No service of this project can take this backup." : undefined}
          />
          {svc && (
            <FloatingInput
              label={`Type ${svc.name} to confirm`}
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              autoComplete="off"
            />
          )}
          <DialogFooter>
            <Button type="submit" variant="destructive" disabled={!svc || typed !== svc.name}>
              Restore
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
