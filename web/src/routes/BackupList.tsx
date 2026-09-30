import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArchiveRestore, Download, ShieldCheck, Trash2 } from "lucide-react";
import { Empty, ErrorText, StateBadge, Tag } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { formatBytes, formatDuration, timeAgo } from "@/lib/format";
import { api, type Backup, type BackupTarget } from "../api";

/** Table of backups with download / restore / delete actions. */
export function BackupList({
  backups,
  targets,
  showService = false,
  restoreInto,
}: {
  backups: Backup[];
  targets: BackupTarget[];
  showService?: boolean;
  /** Name of the database restores overwrite (for the confirmation prompt). */
  restoreInto?: string;
}) {
  const qc = useQueryClient();
  const targetName = (id: string) => targets.find((t) => t.id === id)?.name ?? "deleted target";
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["backups"] });
    qc.invalidateQueries({ queryKey: ["restores"] });
  };
  const remove = useMutation({ mutationFn: api.deleteBackup, onSuccess: invalidate });
  const verify = useMutation({ mutationFn: api.verifyBackup, onSuccess: invalidate });
  const restore = useMutation({
    mutationFn: ({ id, confirm }: { id: string; confirm: string }) => api.restore(id, confirm),
    onSuccess: invalidate,
  });

  if (backups.length === 0) return <Empty>No backups yet.</Empty>;

  const th = "px-2 pb-2 font-medium";
  const td = "px-2 py-2";
  return (
    <div className="space-y-2">
      <div className="-mx-2 overflow-x-auto">
        <table className="w-full text-left text-sm">
          <thead className="text-xs text-muted-foreground">
            <tr className="border-b">
              <th className={th}>Status</th>
              <th className={th}>Restore test</th>
              {showService && <th className={th}>Database</th>}
              <th className={th}>Target</th>
              <th className={th}>Size</th>
              <th className={th}>Took</th>
              <th className={th}>When</th>
              <th className={th}>
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody className="divide-y">
            {backups.map((b) => (
              <tr key={b.id} className="align-top transition-colors hover:bg-muted/50">
                <td className={td}>
                  <StateBadge state={b.status} />
                  {b.error && (
                    <p className="mt-1 max-w-xs truncate text-xs text-destructive" title={b.error}>
                      {b.error}
                    </p>
                  )}
                </td>
                <td className={`${td} text-xs`}>
                  <Verification backup={b} />
                </td>
                {showService && (
                  <td className={`${td} text-xs`}>
                    {b.kind === "manager" ? <span className="italic">manager state</span> : `${b.projectName}/${b.serviceName}`}
                  </td>
                )}
                <td className={`${td} text-xs whitespace-nowrap`}>
                  {targetName(b.targetId)}
                  {b.encrypted && <Tag className="ml-1.5 text-[10px]">age</Tag>}
                </td>
                <td className={`${td} text-xs whitespace-nowrap`}>{b.status === "succeeded" ? formatBytes(b.sizeBytes) : "—"}</td>
                <td className={`${td} text-xs whitespace-nowrap`}>{b.finishedAt ? formatDuration(b.durationMs) : "—"}</td>
                <td className={`${td} text-xs whitespace-nowrap text-muted-foreground`} title={new Date(b.createdAt).toLocaleString()}>
                  {timeAgo(b.createdAt)}
                  {b.pgVersion && <span className="ml-1">· pg {b.pgVersion}</span>}
                </td>
                <td className={`${td} py-1.5`}>
                  <div className="flex justify-end gap-0.5">
                    {b.status === "succeeded" && (
                      <>
                        {b.kind === "postgres" && b.verifyStatus !== "running" && (
                          <Button variant="ghost" size="icon-xs" title="Restore-test" aria-label="Restore-test" onClick={() => verify.mutate(b.id)}>
                            <ShieldCheck />
                          </Button>
                        )}
                        <Button asChild variant="ghost" size="icon-xs" title="Download">
                          <a href={api.downloadUrl(b.id)} aria-label="Download">
                            <Download />
                          </a>
                        </Button>
                        {restoreInto && b.kind === "postgres" && (
                          <ConfirmDialog
                            trigger={
                              <Button variant="ghost" size="icon-xs" title="Restore" aria-label="Restore">
                                <ArchiveRestore />
                              </Button>
                            }
                            title={`Restore into ${restoreInto}?`}
                            description="Current data will be replaced; linked apps are stopped meanwhile."
                            confirmLabel="Restore"
                            typeToConfirm={restoreInto}
                            onConfirm={(name) => restore.mutate({ id: b.id, confirm: name })}
                          />
                        )}
                      </>
                    )}
                    {b.status !== "running" && (
                      <ConfirmDialog
                        trigger={
                          <Button
                            variant="ghost"
                            size="icon-xs"
                            title="Delete"
                            aria-label="Delete"
                            className="text-muted-foreground hover:text-destructive"
                          >
                            <Trash2 />
                          </Button>
                        }
                        title="Delete this backup?"
                        description="It is removed from its target permanently."
                        onConfirm={() => remove.mutate(b.id)}
                      />
                    )}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <ErrorText error={remove.error ?? restore.error ?? verify.error} />
    </div>
  );
}

function Verification({ backup: b }: { backup: Backup }) {
  if (b.kind !== "postgres" || b.status !== "succeeded") return <span className="text-muted-foreground">—</span>;
  if (!b.verifyStatus) return <span className="text-muted-foreground">not tested</span>;
  if (b.verifyStatus === "running") return <StateBadge state="running" />;
  const d = b.verifyDetails;
  const title =
    b.verifyStatus === "succeeded"
      ? `Restored in ${formatDuration(d.durationMs)}: ${d.tables} tables, ~${d.rows.toLocaleString()} rows, ${formatBytes(d.dbBytes)}`
      : b.verifyError;
  return (
    <span title={title} className="inline-flex flex-wrap items-center gap-1.5">
      <StateBadge state={b.verifyStatus === "succeeded" ? "passed" : "failed"} />
      {b.verifyStatus === "succeeded" && (
        <span className="text-muted-foreground">
          {d.tables} tables · {timeAgo(b.verifiedAt!)}
        </span>
      )}
    </span>
  );
}
