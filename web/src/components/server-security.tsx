import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleCheck, CircleHelp, CircleX, RefreshCw, TriangleAlert, type LucideIcon } from "lucide-react";
import { Empty, Section } from "@/components/common";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { timeAgo } from "@/lib/format";
import { cn } from "@/lib/utils";
import { api, type SecurityStatus } from "@/api";

const statusIcon: Record<SecurityStatus, { icon: LucideIcon; className: string; label: string }> = {
  ok: { icon: CircleCheck, className: "text-emerald-600 dark:text-emerald-400", label: "OK" },
  warn: { icon: TriangleAlert, className: "text-amber-600 dark:text-amber-400", label: "Warning" },
  fail: { icon: CircleX, className: "text-destructive", label: "Problem" },
  unknown: { icon: CircleHelp, className: "text-muted-foreground", label: "Unknown" },
};

/** The host's security checklist, checked every six hours or on demand. */
export function ServerSecuritySection({ serverId }: { serverId: string }) {
  const qc = useQueryClient();
  const report = useQuery({ queryKey: ["server-security", serverId], queryFn: () => api.serverSecurity(serverId) });
  const check = useMutation({
    meta: { error: "Couldn't check the server" },
    mutationFn: () => api.checkServerSecurity(serverId),
    onSuccess: (r) => qc.setQueryData(["server-security", serverId], r),
  });
  const r = report.data;
  const checked = r?.checkedAt && !r.checkedAt.startsWith("0001");
  const problems = r?.items.filter((it) => it.status === "fail" || it.status === "warn").length ?? 0;

  return (
    <Section
      title="Security"
      description={
        checked
          ? `${r.os ? `${r.os}. ` : ""}${problems ? `${problems} to look at` : "Nothing to fix"}, checked ${timeAgo(r.checkedAt)}. Checked every six hours.`
          : "Updates, SSH, firewall and open ports of the host, checked every six hours."
      }
      actions={
        <Button variant="outline" size="sm" disabled={check.isPending} onClick={() => check.mutate()}>
          {check.isPending ? <Spinner data-icon="inline-start" /> : <RefreshCw data-icon="inline-start" />}
          Check now
        </Button>
      }
    >
      {report.isPending ? null : r?.error ? (
        <p role="alert" className="text-sm text-destructive">
          Couldn't check the host: {r.error}
        </p>
      ) : !checked ? (
        <Empty>Not checked yet.</Empty>
      ) : (
        <ul className="-my-2 divide-y">
          {r.items.map((it) => {
            const s = statusIcon[it.status];
            return (
              <li key={it.id} className="flex gap-3 py-2.5 text-sm">
                <s.icon className={cn("mt-0.5 size-4 shrink-0", s.className)} aria-label={s.label} />
                <div className="min-w-0 space-y-0.5">
                  <p className="font-medium">{it.title}</p>
                  {it.detail && <p className="text-muted-foreground">{it.detail}</p>}
                  {it.hint && it.status !== "ok" && <p className="text-muted-foreground">{it.hint}</p>}
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </Section>
  );
}
