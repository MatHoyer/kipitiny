import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Cloud, DatabaseBackup, HardDrive, History, Search, TriangleAlert } from "lucide-react";
import { useState } from "react";
import { EmptyState, ErrorText, Section, StatCard } from "@/components/common";
import { PageBody, PageHeader } from "@/components/page-header";
import { Spinner } from "@/components/ui/spinner";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useTab } from "@/hooks/use-tab";
import { formatBytes, timeAgo } from "@/lib/format";
import { api, type Backup, type BackupTarget } from "../api";
import { BackupList } from "./BackupList";
import { BackupNowDialog } from "@/components/backup-now-dialog";
import { Schedules } from "./Schedules";


export function Backups() {
  const targets = useQuery({ queryKey: ["storage"], queryFn: api.backupTargets });
  const backups = useQuery({
    queryKey: ["backups", "all"],
    queryFn: () => api.backups(),
    refetchInterval: (q) => (q.state.data?.some((b) => b.status === "running") ? 1_000 : 15_000),
  });

  const [tab, setTab] = useTab(["backups", "manager"], "backups");
  const all = backups.data ?? [];

  return (
    <>
      <PageHeader crumbs={[{ label: "Backups" }]} />
      <PageBody>
        <BackupStats backups={backups.data} targets={targets.data?.length} />
        <Tabs value={tab} onValueChange={setTab}>
          <TabsList variant="line">
            <TabsTrigger value="backups">Backups</TabsTrigger>
            <TabsTrigger value="manager">Manager state</TabsTrigger>
          </TabsList>
          <TabsContent value="backups" className="space-y-6">
            <ErrorText error={backups.error} />
            <FilteredBackups backups={all.filter((b) => b.kind === "postgres")} targets={targets.data ?? []} />
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
  const backup = async (targetId: string) => {
    await api.backupManager(targetId);
    qc.invalidateQueries({ queryKey: ["backups"] });
  };
  return (
    <Section
      plain
      description="Projects, services, schedules, credentials and keys live in one SQLite file. Snapshot it on a schedule, and keep a copy off-site."
      actions={<BackupNowDialog what="the manager" targets={targets} sensitive onBackup={backup} />}
    >
      <Schedules targets={targets} />
    </Section>
  );
}
