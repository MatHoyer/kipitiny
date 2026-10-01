import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Section } from "@/components/common";
import { timeAgo } from "@/lib/format";
import { api, type Status } from "@/api";
import { SettingsPage } from "./page";

/** The running version, with an on-demand check for a newer one. */
export function Version() {
  const qc = useQueryClient();
  const status = useQuery({ queryKey: ["status"], queryFn: api.status, refetchInterval: 15_000 });
  const check = useMutation({
    meta: { error: "Couldn't check for updates" },
    mutationFn: api.checkUpdate,
    onSuccess: (update) => qc.setQueryData<Status>(["status"], (s) => s && { ...s, update }),
  });
  const update = status.data?.update;

  return (
    <SettingsPage
      actions={
        <Button variant="outline" size="sm" disabled={check.isPending} onClick={() => check.mutate()}>
          {check.isPending ? "Checking…" : "Check for updates"}
        </Button>
      }
    >
      <Section plain>
        {update && (
          <div className="space-y-1 text-sm">
            <p>
              Running <span className="font-mono">{update.current}</span>
              {update.available ? (
                <>
                  {" "}
                  · <span className="font-mono">{update.latest}</span> is available
                </>
              ) : (
                update.latest && " · up to date"
              )}
            </p>
            {update.available && !update.canApply && <p className="text-muted-foreground">{update.reason}</p>}
            {update.checkedAt && <p className="text-xs text-muted-foreground">Last check {timeAgo(update.checkedAt)}</p>}
            {update.error && <p className="text-sm text-destructive">{update.error}</p>}
          </div>
        )}
      </Section>
    </SettingsPage>
  );
}
