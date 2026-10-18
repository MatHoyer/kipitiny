import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, ChevronLeft, CircleCheck, DatabaseBackup, Lock, TriangleAlert } from "lucide-react";
import { useState, type ReactNode } from "react";
import { Link } from "react-router";
import { toast } from "sonner";
import { ChoiceTile, Tag } from "@/components/common";
import { BackupSteps, useLastStep } from "@/components/op-steps";
import { storageIcon, storageLocation } from "@/components/storage";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { FloatingSelect } from "@/components/ui/floating-select";
import { formatBytes, formatDuration } from "@/lib/format";
import { api, type Backup, type BackupTarget, type PgDatabase } from "@/api";

/**
 * "Back up now" in two steps: pick a storage, then review and start. With a
 * single storage the picker is skipped. A single backup is then followed
 * step by step; several (a project's) run in the background.
 */
export function BackupNowDialog({
  what,
  targets,
  onBackup,
  sensitive,
  disabled,
  databases = [],
  variant = "default",
}: {
  /** What gets backed up, e.g. "shop/db". */
  what: ReactNode;
  targets: BackupTarget[];
  onBackup: (targetId: string, database: string) => Promise<unknown>;
  /** A PostgreSQL instance's databases: with several, the user picks one (the main by default). */
  databases?: PgDatabase[];
  /** The backup holds secrets: warn when the storage isn't encrypted. */
  sensitive?: boolean;
  disabled?: boolean;
  variant?: "default" | "outline";
}) {
  const [open, setOpen] = useState(false);
  const [picked, setPicked] = useState<BackupTarget | null>(null);
  const [database, setDatabase] = useState("");
  const db = database || databases.find((d) => d.main)?.name || "";
  const single = targets.length === 1 ? targets[0] : null;
  const target = picked ?? single;
  const qc = useQueryClient();
  const [started, setStarted] = useState<string | null>(null);
  const backup = useMutation({
    meta: { error: "Couldn't start the backup" },
    mutationFn: () => onBackup(target!.id, db),
    onSuccess: (b) => {
      if (b && typeof b === "object" && !Array.isArray(b) && "id" in b) setStarted((b as Backup).id);
      else {
        toast.success("Backup started");
        onOpenChange(false);
      }
    },
  });
  const onOpenChange = (next: boolean) => {
    setOpen(next);
    if (!next) {
      setPicked(null);
      setDatabase("");
      setStarted(null);
      backup.reset();
      qc.invalidateQueries({ queryKey: ["backups"] });
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button size="sm" variant={variant} disabled={disabled || targets.length === 0}>
          <DatabaseBackup data-icon="inline-start" />
          Back up now
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-lg">
        {started ? (
          <BackupProgress id={started} what={what} onClose={() => onOpenChange(false)} />
        ) : !target ? (
          <>
            <DialogHeader>
              <DialogTitle>Back up {what}</DialogTitle>
              <DialogDescription>Where should the backup go?</DialogDescription>
            </DialogHeader>
            <div className="grid gap-3 sm:grid-cols-2">
              {targets.map((t) => (
                <ChoiceTile
                  key={t.id}
                  icon={storageIcon(t)}
                  title={t.name}
                  description={
                    <span className="block truncate font-mono" title={storageLocation(t)}>
                      {storageLocation(t)}
                    </span>
                  }
                  badge={t.ageRecipient && <Tag className="bg-emerald-500/10 text-emerald-600">encrypted</Tag>}
                  onClick={() => setPicked(t)}
                />
              ))}
            </div>
          </>
        ) : (
          <>
            <DialogHeader>
              <DialogTitle>Back up {what}</DialogTitle>
              <DialogDescription>It runs in the background and shows up in the history.</DialogDescription>
            </DialogHeader>
            <div className="flex items-start gap-3 rounded-xl border p-4">
              <span className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-muted [&>svg]:size-5">{storageIcon(target)}</span>
              <div className="min-w-0 flex-1">
                <p className="truncate font-medium">{target.name}</p>
                <p className="truncate font-mono text-xs text-muted-foreground" title={storageLocation(target)}>
                  {storageLocation(target)}
                </p>
              </div>
            </div>
            {databases.length > 1 && (
              <FloatingSelect
                label="Database"
                value={db}
                onValueChange={setDatabase}
                description="A backup holds one database of the instance."
                options={databases.map((d) => ({
                  value: d.name,
                  label: (
                    <span className="flex items-center gap-2">
                      <span className="font-mono">{d.name}</span>
                      {d.main && <span className="text-muted-foreground">main</span>}
                    </span>
                  ),
                }))}
              />
            )}
            {target.ageRecipient ? (
              <p className="flex items-center gap-1.5 text-sm text-muted-foreground">
                <Lock className="size-4 shrink-0 text-emerald-600" />
                Encrypted: only this storage's key can read it.
              </p>
            ) : (
              sensitive && (
                <p className="flex items-center gap-1.5 text-sm text-muted-foreground">
                  <TriangleAlert className="size-4 shrink-0 text-amber-500" />
                  Not encrypted, and it holds every secret the manager knows.
                </p>
              )
            )}
            <DialogFooter>
              {!single && (
                <Button variant="ghost" className="sm:mr-auto" disabled={backup.isPending} onClick={() => setPicked(null)}>
                  <ChevronLeft data-icon="inline-start" />
                  Back
                </Button>
              )}
              <Button loading={backup.isPending} onClick={() => backup.mutate()}>
                <DatabaseBackup data-icon="inline-start" />
                Back up
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

/** A backup just started, followed until it ends. */
function BackupProgress({ id, what, onClose }: { id: string; what: ReactNode; onClose: () => void }) {
  const q = useQuery({
    queryKey: ["backups", "one", id],
    queryFn: () => api.getBackup(id),
    refetchInterval: (q) => (q.state.data?.status === "succeeded" || q.state.data?.status === "failed" ? false : 1_000),
  });
  const b = q.data;
  const running = !b || b.status === "running";
  const seen = useLastStep(b?.status === "running", b?.step);
  return (
    <>
      <DialogHeader>
        <DialogTitle>{running ? <>Backing up {what}</> : b.status === "succeeded" ? <>Backed up {what}</> : <>Backup of {what} failed</>}</DialogTitle>
        <DialogDescription>
          {running ? "You can close this: it keeps running and shows up in the history." : "It's in the history with the others."}
        </DialogDescription>
      </DialogHeader>
      {b && <BackupSteps backup={b} step={seen?.step} />}
      {b?.status === "succeeded" && (
        <p className="flex items-center gap-2 text-sm animate-in fade-in">
          <CircleCheck className="size-4 text-emerald-600" />
          {formatBytes(b.sizeBytes)} in {formatDuration(b.durationMs)}
          {b.encrypted && <Tag className="bg-emerald-500/10 text-emerald-600">encrypted</Tag>}
        </p>
      )}
      {b?.status === "failed" && <p className="text-sm text-destructive animate-in fade-in">{b.error}</p>}
      <DialogFooter>
        <Button variant="ghost" onClick={onClose}>
          Close
        </Button>
        <Button asChild variant="outline">
          <Link to={`/backups/${id}`} onClick={onClose}>
            Open backup
            <ArrowRight data-icon="inline-end" />
          </Link>
        </Button>
      </DialogFooter>
    </>
  );
}
