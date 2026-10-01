import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowUpCircle, Container, ExternalLink, Package, RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ErrorText, IconTile, Loading, StatCard } from "@/components/common";
import { Card } from "@/components/ui/card";
import { timeAgo } from "@/lib/format";
import { api, type Status } from "@/api";
import { SettingsPage } from "./page";

/** The running version and Docker engine, with an on-demand check for a newer release. */
export function Version() {
  const qc = useQueryClient();
  const status = useQuery({ queryKey: ["status"], queryFn: api.status, refetchInterval: 15_000 });
  const check = useMutation({
    meta: { error: "Couldn't check for updates" },
    mutationFn: api.checkUpdate,
    onSuccess: (update) => qc.setQueryData<Status>(["status"], (s) => s && { ...s, update }),
  });
  const s = status.data;
  const update = s?.update;

  return (
    <SettingsPage
      actions={
        <Button variant="outline" size="sm" loading={check.isPending} onClick={() => check.mutate()}>
          <RefreshCw data-icon="inline-start" />
          Check for updates
        </Button>
      }
    >
      <ErrorText error={status.error} />
      {!s || !update ? (
        !status.error && <Loading />
      ) : (
        <>
          <div className="grid grid-cols-2 gap-4 md:grid-cols-3">
            <StatCard
              icon={Package}
              label="kipitiny"
              value={<span className="font-mono">{update.current}</span>}
              hint={update.available ? `${update.latest} available` : update.latest ? "Up to date" : "Not checked yet"}
              tone={update.available ? "warn" : update.latest ? "good" : undefined}
            />
            <StatCard
              icon={Container}
              label="Docker"
              value={s.docker ? <span className="font-mono">{s.docker.version}</span> : "Unreachable"}
              hint={s.docker ? `API ${s.docker.apiVersion} · ${s.docker.os}/${s.docker.arch}` : s.dockerError}
              tone={s.docker ? undefined : "bad"}
            />
            <StatCard
              icon={RefreshCw}
              label="Last check"
              value={update.checkedAt ? timeAgo(update.checkedAt) : "never"}
              hint={update.latest && `Latest release ${update.latest}`}
            />
          </div>
          {update.available && (
            <Card className="gap-3 px-4">
              <div className="flex flex-wrap items-start gap-3">
                <IconTile icon={ArrowUpCircle} />
                <div className="min-w-0 flex-1 space-y-0.5">
                  <p className="font-medium">
                    <span className="font-mono">{update.latest}</span> is available
                  </p>
                  <p className="text-xs text-muted-foreground">
                    {update.canApply ? "Update from the banner in the sidebar; apps and databases keep running." : update.reason}
                  </p>
                </div>
                <Button variant="outline" size="sm" asChild>
                  <a href={`https://github.com/MatHoyer/kipitiny/releases/tag/${update.latest}`} target="_blank" rel="noreferrer">
                    Release notes
                    <ExternalLink data-icon="inline-end" />
                  </a>
                </Button>
              </div>
            </Card>
          )}
          {update.error && <p className="text-sm text-destructive">{update.error}</p>}
        </>
      )}
    </SettingsPage>
  );
}
