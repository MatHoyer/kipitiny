import { ChevronRight, DatabaseBackup, HardDrive, ShieldAlert, ShieldCheck } from "lucide-react";
import type { ReactNode } from "react";
import { Link } from "react-router";
import { ServiceIcon } from "@/components/service-icon";
import { EmptyState, StateBadge, Tag } from "@/components/common";
import { byDay, formatBytes, formatDuration, timeAgo } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { Backup, BackupTarget } from "../api";
import { Tip } from "@/components/ui/tooltip";

/** Backups as rows grouped by day; each opens its page. */
export function BackupList({
  backups,
  targets,
  showService = false,
  empty,
}: {
  backups: Backup[];
  targets: BackupTarget[];
  showService?: boolean;
  empty?: ReactNode;
}) {
  if (backups.length === 0)
    return (
      empty ?? <EmptyState icon={DatabaseBackup} title="No backups yet" description="Back up a database or volumes, or let a schedule do it." />
    );
  const targetName = (id: string) => targets.find((t) => t.id === id)?.name ?? "deleted target";

  return (
    <div className="space-y-6">
      {byDay(backups).map(([day, rows]) => (
        <section key={day} className="space-y-2">
          <h3 className="text-xs font-medium tracking-wide text-muted-foreground uppercase">{day}</h3>
          <ul className="divide-y overflow-hidden rounded-xl border">
            {rows.map((b) => (
              <li key={b.id}>
                <Link
                  to={`/backups/${b.id}`}
                  className="group flex items-center gap-3 px-4 py-3 transition-colors outline-none hover:bg-muted/50 focus-visible:bg-muted/50"
                >
                  <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted">
                    <BackupIcon backup={b} className="size-4.5" />
                  </span>
                  <div className="min-w-0 flex-1">
                    <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
                      <span className="truncate">{backupTitle(b, showService)}</span>
                      {b.status !== "succeeded" && <StateBadge state={b.status} />}
                    </p>
                    <p className="truncate text-xs text-muted-foreground">
                      {new Date(b.createdAt).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })} · {targetName(b.targetId)}
                      {b.finishedAt && b.status === "succeeded" && ` · ${formatDuration(b.durationMs)}`}
                      {b.error && <span className="text-destructive"> · {b.error}</span>}
                    </p>
                  </div>
                  <div className="flex shrink-0 items-center gap-2 max-sm:hidden">
                    {b.encrypted && <Tag>age</Tag>}
                    <Verification backup={b} />
                  </div>
                  <span className="w-16 shrink-0 text-right text-sm tabular-nums">
                    {b.status === "succeeded" ? formatBytes(b.sizeBytes) : "—"}
                  </span>
                  <ChevronRight className="size-4 shrink-0 text-muted-foreground transition-transform group-hover:translate-x-0.5" />
                </Link>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

export function BackupIcon({ backup: b, className }: { backup: Backup; className?: string }) {
  if (b.kind === "manager") return <DatabaseBackup className={className} />;
  const kind = b.serviceKind ?? (b.kind === "postgres" ? "postgres" : undefined);
  return <ServiceIcon service={{ icon: b.serviceIcon, kind }} fallback={HardDrive} className={className} />;
}

export function backupTitle(b: Backup, withProject = true) {
  if (b.kind === "manager") return "Manager state";
  return withProject ? `${b.projectName} / ${b.serviceName}` : b.serviceName;
}

/** The restore test's outcome, compact. */
export function Verification({ backup: b, className }: { backup: Backup; className?: string }) {
  if (b.kind === "manager" || b.status !== "succeeded") return null;
  if (b.verifyStatus === "running") return <StateBadge state="running" className={className} />;
  if (!b.verifyStatus) return <span className={cn("text-xs text-muted-foreground", className)}>not tested</span>;
  const ok = b.verifyStatus === "succeeded";
  return (
    <Tip content={ok ? `Restore test passed ${timeAgo(b.verifiedAt!)}` : b.verifyError}>
      <span
        className={cn(
          "inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs",
          ok ? "bg-emerald-500/10 text-emerald-700 dark:text-emerald-400" : "bg-destructive/10 text-destructive",
          className,
        )}
      >
        {ok ? <ShieldCheck className="size-3.5" /> : <ShieldAlert className="size-3.5" />}
        {ok ? "tested" : "test failed"}
      </span>
    </Tip>
  );
}
