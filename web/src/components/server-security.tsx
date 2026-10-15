import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleCheck, CircleHelp, CircleX, RefreshCw, TriangleAlert, Wrench, type LucideIcon } from "lucide-react";
import { Empty, Section } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { Stepper } from "@/components/ui/stepper";
import { timeAgo } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { ReactNode } from "react";
import { api, type FixRun, type SecurityStatus } from "@/api";

const statusIcon: Record<SecurityStatus, { icon: LucideIcon; className: string; label: string }> = {
  ok: { icon: CircleCheck, className: "text-emerald-600 dark:text-emerald-400", label: "OK" },
  warn: { icon: TriangleAlert, className: "text-amber-600 dark:text-amber-400", label: "Warning" },
  fail: { icon: CircleX, className: "text-destructive", label: "Problem" },
  unknown: { icon: CircleHelp, className: "text-muted-foreground", label: "Unknown" },
};

/** What each fix does on the host, as the steps and the confirmation say it. */
const fixes: Record<string, { title: string; does: string }> = {
  updates: {
    title: "Install the security updates",
    does: "Installs the pending security updates only. Docker's packages are held back, so no app restarts; a kernel update then waits for a reboot.",
  },
  auto_updates: {
    title: "Turn on automatic security updates",
    does: "Installs unattended-upgrades (dnf-automatic on dnf hosts) for daily security updates. They never reboot the server.",
  },
  fail2ban: { title: "Install fail2ban", does: "Bans addresses that keep failing SSH logins, on every port, so it keeps working if the SSH port changes." },
  firewall: {
    title: "Turn on the ufw firewall",
    does: "Allows every port SSH listens on, 80 and 443, then denies other incoming traffic. Ports published by Docker aren't affected; anything else listed under Open ports stops being reachable.",
  },
  ssh_password: {
    title: "Turn off SSH passwords",
    does: "Only keys can log in afterwards. Make sure your own key works first: password logins fail from now on.",
  },
  ssh_root: { title: "Keys only for root", does: "root can still log in over SSH with a key, never with a password." },
};

/** The host's security checklist, checked every six hours or on demand, with what kipitiny can fix. */
export function ServerSecuritySection({ serverId }: { serverId: string }) {
  const qc = useQueryClient();
  const key = ["server-security", serverId];
  const report = useQuery({
    queryKey: key,
    queryFn: () => api.serverSecurity(serverId),
    refetchInterval: (q) => (q.state.data?.fix && !q.state.data.fix.finishedAt ? 2000 : false),
  });
  const check = useMutation({
    meta: { error: "Couldn't check the server" },
    mutationFn: () => api.checkServerSecurity(serverId),
    onSuccess: (r) => qc.setQueryData(key, r),
  });
  const fix = useMutation({
    meta: { error: "Couldn't start the fixes" },
    mutationFn: (ids: string[]) => api.fixServerSecurity(serverId, ids),
    onSuccess: (r) => qc.setQueryData(key, r),
  });
  const r = report.data;
  const checked = r?.checkedAt && !r.checkedAt.startsWith("0001");
  const fixing = !!r?.fix && !r.fix.finishedAt;
  const problems = r?.items.filter((it) => it.status === "fail" || it.status === "warn").length ?? 0;
  const fixable = r?.items.filter((it) => it.fix).map((it) => it.id) ?? [];
  const busy = fixing || fix.isPending;

  return (
    <Section
      title="Security"
      description={
        checked
          ? [r.os, problems ? `${problems} to look at` : "nothing to fix", `checked ${timeAgo(r.checkedAt)}`].filter(Boolean).join(" · ")
          : "Updates, SSH, firewall and open ports, checked every six hours."
      }
      actions={
        <>
          {fixable.length > 1 && (
            <FixDialog ids={fixable} disabled={busy} onConfirm={() => fix.mutate(fixable)}>
              <Wrench data-icon="inline-start" />
              Fix all ({fixable.length})
            </FixDialog>
          )}
          <Button variant="outline" size="sm" disabled={check.isPending || busy} onClick={() => check.mutate()}>
            {check.isPending ? <Spinner data-icon="inline-start" /> : <RefreshCw data-icon="inline-start" />}
            Check now
          </Button>
        </>
      }
    >
      {r?.fix && <FixProgress run={r.fix} step={r.fixStep} />}
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
                <div className="min-w-0 flex-1 space-y-0.5">
                  <p className="font-medium">{it.title}</p>
                  {it.detail && <p className="text-muted-foreground">{it.detail}</p>}
                  {it.hint && it.status !== "ok" && <p className="text-muted-foreground">{it.hint}</p>}
                  {it.noFix && <p className="text-xs text-muted-foreground italic">No automatic fix: {it.noFix}.</p>}
                </div>
                {it.fix && (
                  <FixDialog ids={[it.id]} variant="outline" disabled={busy} onConfirm={() => fix.mutate([it.id])}>
                    Fix…
                  </FixDialog>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </Section>
  );
}

/** A button asking to confirm what the fixes ids will do on the host. */
function FixDialog({
  ids,
  variant,
  disabled,
  onConfirm,
  children,
}: {
  ids: string[];
  variant?: "outline";
  disabled: boolean;
  onConfirm: () => void;
  children: ReactNode;
}) {
  const one = ids.length === 1 ? fixes[ids[0]] : undefined;
  return (
    <ConfirmDialog
      trigger={
        <Button size="sm" variant={variant} disabled={disabled} className={cn(one && "shrink-0 self-start")}>
          {children}
        </Button>
      }
      title={one ? `${one.title}?` : `Fix these ${ids.length} points on the server?`}
      description={
        one ? (
          one.does
        ) : (
          <ul className="list-disc space-y-1.5 pl-5">
            {ids.map((id) => (
              <li key={id}>
                <span className="font-medium text-foreground">{fixes[id]?.title ?? id}.</span> {fixes[id]?.does}
              </li>
            ))}
          </ul>
        )
      }
      confirmLabel="Fix"
      destructive={false}
      onConfirm={onConfirm}
    />
  );
}

/** A fix run step by step while it runs, then what failed in it. */
function FixProgress({ run, step }: { run: FixRun; step?: string }) {
  const running = !run.finishedAt;
  const failures = run.results.filter((x) => x.error);
  if (!running && !run.error && failures.length === 0) return null;
  const steps = [
    { id: "read", title: "Read the host", hint: "Its package manager, SSH port and keys, now" },
    ...run.ids.map((id) => ({ id, title: fixes[id]?.title ?? id, hint: fixes[id]?.does })),
    { id: "check", title: "Check again" },
  ];
  if (running)
    return (
      <div className="mb-4 rounded-lg border p-4">
        <Stepper orientation="vertical" steps={steps} current={Math.max(steps.findIndex((s) => s.id === step), 0)} busy />
      </div>
    );
  return (
    <div role="alert" className="mb-4 space-y-2 rounded-lg border border-destructive/40 p-4 text-sm">
      <p className="font-medium">Last fix, {timeAgo(run.finishedAt!)}:</p>
      {run.error && <p className="text-destructive">{run.error}</p>}
      {failures.map((x) => (
        <div key={x.id} className="space-y-1">
          <p>
            <span className="font-medium">{fixes[x.id]?.title ?? x.id}</span> failed: <span className="text-destructive">{x.error}</span>
          </p>
          {x.output && <pre className="max-h-40 overflow-auto rounded bg-muted p-2 text-xs whitespace-pre-wrap">{x.output}</pre>}
        </div>
      ))}
    </div>
  );
}
