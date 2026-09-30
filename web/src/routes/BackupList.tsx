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
                {showService && (
                  <td className="py-1.5 text-xs">
                    {b.projectName}/{b.serviceName}
                  </td>
                )}
                <td className="py-1.5 text-xs">{targetName(b.targetId)}</td>
                <td className="py-1.5 text-xs">{b.status === "succeeded" ? formatBytes(b.sizeBytes) : "—"}</td>
                <td className="py-1.5 text-xs">{b.finishedAt ? formatDuration(b.durationMs) : "—"}</td>
                <td className="py-1.5 text-xs text-zinc-500" title={new Date(b.createdAt).toLocaleString()}>
                  {timeAgo(b.createdAt)}
                  {b.pgVersion && <span className="ml-1">· pg {b.pgVersion}</span>}
                </td>
                <td className="space-x-3 py-1.5 text-right text-xs whitespace-nowrap">
                  {b.status === "succeeded" && (
                    <>
                      <a href={api.downloadUrl(b.id)} className="hover:underline">
                        Download
                      </a>
                      {restoreInto && (
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
      <ErrorText error={remove.error ?? restore.error} />
    </div>
  );
}
