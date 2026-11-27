import { useQuery, useQueryClient } from "@tanstack/react-query";
import { StateBadge } from "@/components/common";
import { Section } from "@kipit-labs/kipitidy/section";
import { BackupNowDialog } from "@/features/backups/backup-now-dialog";
import { timeAgo } from "@/lib/format";
import { api, type Service as ServiceT } from "../../api";
import { BackupList } from "../backups/BackupList";
import { Schedules } from "../backups/Schedules";

export function BackupsCard({
  serviceId,
  name,
  kind,
  canBackUp,
}: {
  serviceId: string;
  name: string;
  kind: ServiceT["kind"];
  canBackUp: boolean;
}) {
  const qc = useQueryClient();
  const targets = useQuery({ queryKey: ["storage"], queryFn: api.backupTargets });
  // A PostgreSQL instance may hold several databases; a backup picks one. Stopped: no list, the main one.
  const databases = useQuery({
    queryKey: ["data", serviceId, "databases"],
    queryFn: () => api.pgDatabases(serviceId),
    enabled: kind === "postgres",
    retry: false,
    meta: { error: false },
  });
  const backups = useQuery({
    queryKey: ["backups", serviceId],
    queryFn: () => api.serviceBackups(serviceId),
    refetchInterval: (q) => (q.state.data?.some((b) => b.status === "running") ? 1_000 : 15_000),
  });
  const restores = useQuery({
    queryKey: ["restores", serviceId],
    queryFn: () => api.restores(serviceId),
    refetchInterval: (q) => (q.state.data?.some((r) => r.status === "running") ? 1_000 : 15_000),
  });
  const backup = async (targetId: string, database: string) => {
    const b = await api.backup(serviceId, targetId, database);
    qc.invalidateQueries({ queryKey: ["backups"] });
    return b;
  };
  const lastRestore = restores.data?.[0];
  const busy = backups.data?.some((b) => b.status === "running") || lastRestore?.status === "running";

  return (
    <Section
      plain
      actions={
        canBackUp && <BackupNowDialog what={name} targets={targets.data ?? []} disabled={busy} onBackup={backup} databases={databases.data} />
      }
    >
      {lastRestore && (
        <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
          Last restore <StateBadge state={lastRestore.status} /> {timeAgo(lastRestore.createdAt)}
          {lastRestore.error && <span className="truncate text-destructive">{lastRestore.error}</span>}
        </div>
      )}
      <Schedules serviceId={serviceId} targets={targets.data ?? []} databases={databases.data} />
      <div className="space-y-2">
        <h3 className="text-sm font-medium">History</h3>
        <BackupList backups={backups.data ?? []} targets={targets.data ?? []} />
      </div>
    </Section>
  );
}
