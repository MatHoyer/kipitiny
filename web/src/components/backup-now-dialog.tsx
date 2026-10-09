import { useMutation } from "@tanstack/react-query";
import { ChevronLeft, DatabaseBackup, Lock, TriangleAlert } from "lucide-react";
import { useState, type ReactNode } from "react";
import { toast } from "sonner";
import { ChoiceTile, Tag } from "@/components/common";
import { storageIcon, storageLocation } from "@/components/storage";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import type { BackupTarget } from "@/api";

/**
 * "Back up now" in two steps: pick a storage, then review and start. With a
 * single storage the picker is skipped.
 */
export function BackupNowDialog({
  what,
  targets,
  onBackup,
  sensitive,
  disabled,
  variant = "default",
}: {
  /** What gets backed up, e.g. "shop/db". */
  what: ReactNode;
  targets: BackupTarget[];
  onBackup: (targetId: string) => Promise<unknown>;
  /** The backup holds secrets: warn when the storage isn't encrypted. */
  sensitive?: boolean;
  disabled?: boolean;
  variant?: "default" | "outline";
}) {
  const [open, setOpen] = useState(false);
  const [picked, setPicked] = useState<BackupTarget | null>(null);
  const single = targets.length === 1 ? targets[0] : null;
  const target = picked ?? single;
  const backup = useMutation({
    meta: { error: "Couldn't start the backup" },
    mutationFn: () => onBackup(target!.id),
    onSuccess: () => {
      toast.success("Backup started");
      onOpenChange(false);
    },
  });
  const onOpenChange = (next: boolean) => {
    setOpen(next);
    if (!next) {
      setPicked(null);
      backup.reset();
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
      <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-lg">
        {!target ? (
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
