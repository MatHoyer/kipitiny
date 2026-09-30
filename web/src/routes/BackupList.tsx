import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, type Backup, type BackupTarget } from "../api";
import { confirmByName, ErrorText, formatBytes, formatDuration, StateBadge, timeAgo } from "../ui";

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

  if (backups.length === 0) return <p className="text-sm text-zinc-500">No backups yet.</p>;

  return (
    <div className="space-y-2">
      <div className="overflow-x-auto">
        <table className="w-full text-left text-sm">
          <thead className="text-xs text-zinc-500">
            <tr>
              <th className="pb-2 font-medium">Status</th>
              <th className="pb-2 font-medium">Restore test</th>
              {showService && <th className="pb-2 font-medium">Database</th>}
              <th className="pb-2 font-medium">Target</th>
              <th className="pb-2 font-medium">Size</th>
              <th className="pb-2 font-medium">Took</th>
              <th className="pb-2 font-medium">When</th>
              <th />
            </tr>
          </thead>
          <tbody className="divide-y divide-zinc-200 dark:divide-zinc-800">
            {backups.map((b) => (
              <tr key={b.id} className="align-top">
                <td className="py-1.5">
                  <StateBadge state={b.status} />
                  {b.error && <p className="max-w-xs truncate text-xs text-red-600" title={b.error}>{b.error}</p>}
                </td>
                <td className="py-1.5 text-xs">
                  <Verification backup={b} />
                </td>
                {showService && (
                  <td className="py-1.5 text-xs">
                    {b.kind === "manager" ? <span className="italic">manager state</span> : `${b.projectName}/${b.serviceName}`}
                  </td>
                )}
                <td className="py-1.5 text-xs">
                  {targetName(b.targetId)}
                  {b.encrypted && (
                    <span className="ml-1 rounded bg-zinc-100 px-1 text-[10px] text-zinc-600 dark:bg-zinc-800 dark:text-zinc-400">
                      age
                    </span>
                  )}
                </td>
                <td className="py-1.5 text-xs">{b.status === "succeeded" ? formatBytes(b.sizeBytes) : "—"}</td>
                <td className="py-1.5 text-xs">{b.finishedAt ? formatDuration(b.durationMs) : "—"}</td>
                <td className="py-1.5 text-xs text-zinc-500" title={new Date(b.createdAt).toLocaleString()}>
                  {timeAgo(b.createdAt)}
                  {b.pgVersion && <span className="ml-1">· pg {b.pgVersion}</span>}
                </td>
                <td className="space-x-3 py-1.5 text-right text-xs whitespace-nowrap">
                  {b.status === "succeeded" && (
                    <>
                      {b.kind === "postgres" && b.verifyStatus !== "running" && (
                        <button className="hover:underline" onClick={() => verify.mutate(b.id)}>
                          Verify
                        </button>
                      )}
                      <a href={api.downloadUrl(b.id)} className="hover:underline">
                        Download
                      </a>
                      {restoreInto && b.kind === "postgres" && (
                        <button
                          className="hover:underline"
                          onClick={() => {
                            const name = confirmByName(
                              `Restore this backup into ${restoreInto}? Current data will be replaced; linked apps are stopped meanwhile.`,
                              restoreInto,
                            );
                            if (name) restore.mutate({ id: b.id, confirm: name });
                          }}
                        >
                          Restore
                        </button>
                      )}
                    </>
                  )}
                  {b.status !== "running" && (
                    <button
                      className="text-red-600 hover:underline"
                      onClick={() => confirm("Delete this backup permanently?") && remove.mutate(b.id)}
                    >
                      Delete
                    </button>
                  )}
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
  if (b.kind !== "postgres" || b.status !== "succeeded") return <span className="text-zinc-400">—</span>;
  if (!b.verifyStatus) return <span className="text-zinc-400">not tested</span>;
  if (b.verifyStatus === "running") return <StateBadge state="running" />;
  const d = b.verifyDetails;
  const title =
    b.verifyStatus === "succeeded"
      ? `Restored in ${formatDuration(d.durationMs)}: ${d.tables} tables, ~${d.rows.toLocaleString()} rows, ${formatBytes(d.dbBytes)}`
      : b.verifyError;
  return (
    <span title={title}>
      <StateBadge state={b.verifyStatus === "succeeded" ? "passed" : "failed"} />
      {b.verifyStatus === "succeeded" && (
        <span className="ml-1 text-zinc-500">
          {d.tables} tables · {timeAgo(b.verifiedAt!)}
        </span>
      )}
    </span>
  );
}
