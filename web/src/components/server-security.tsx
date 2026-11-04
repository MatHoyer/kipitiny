import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronDown, CircleCheck, CircleHelp, CircleX, Download, Power, RefreshCw, TriangleAlert, type LucideIcon } from "lucide-react";
import { useState } from "react";
import { CheckboxField, Empty, Section } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { logBox } from "@/components/logs";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Spinner } from "@/components/ui/spinner";
import { timeAgo } from "@/lib/format";
import { cn } from "@/lib/utils";
import { api, type PendingUpdate, type SecurityReport, type SecurityStatus, type UpdateImpact } from "@/api";

const statusIcon: Record<SecurityStatus, { icon: LucideIcon; className: string; label: string }> = {
  ok: { icon: CircleCheck, className: "text-emerald-600 dark:text-emerald-400", label: "OK" },
  warn: { icon: TriangleAlert, className: "text-amber-600 dark:text-amber-400", label: "Warning" },
  fail: { icon: CircleX, className: "text-destructive", label: "Problem" },
  unknown: { icon: CircleHelp, className: "text-muted-foreground", label: "Unknown" },
};

const impactLabel: Record<UpdateImpact, string> = { reboot: "needs a reboot", docker: "restarts Docker", ssh: "restarts SSH" };

/** The host's security checklist, checked every six hours or on demand. */
export function ServerSecuritySection({ serverId }: { serverId: string }) {
  const qc = useQueryClient();
  const report = useQuery({
    queryKey: ["server-security", serverId],
    queryFn: () => api.serverSecurity(serverId),
    refetchInterval: (q) => (q.state.data?.install?.running || q.state.data?.rebootingSince ? 5000 : false),
  });
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
      {report.isPending ? null : r?.rebootingSince ? (
        <p className="flex items-center gap-2 text-sm text-muted-foreground">
          <Spinner />
          Rebooting, started {timeAgo(r.rebootingSince)}. The host is checked again once it's back.
        </p>
      ) : r?.error ? (
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
                  {it.id === "updates" && it.status !== "unknown" && <Updates serverId={serverId} report={r} />}
                  {it.id === "reboot" && r.reboot && <RebootButton serverId={serverId} disabled={!!r.install?.running} />}
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </Section>
  );
}

/** The pending updates with their impact, the install button and the last install. */
function Updates({ serverId, report }: { serverId: string; report: SecurityReport }) {
  const qc = useQueryClient();
  const install = useMutation({
    meta: { error: "Couldn't start installing the updates" },
    mutationFn: (all: boolean) => api.installServerUpdates(serverId, all),
    onSuccess: (r) => qc.setQueryData(["server-security", serverId], r),
  });
  const run = report.install;
  const running = run?.running || install.isPending;
  const { updates } = report;

  return (
    <div className="space-y-2 pt-1.5">
      {running ? (
        <p className="flex items-center gap-2 text-muted-foreground">
          <Spinner />
          Installing {run?.all ? "every update" : "security updates"}
          {run?.startedAt && `, started ${timeAgo(run.startedAt)}`}…
        </p>
      ) : (
        updates.length > 0 && (
          <div className="flex flex-wrap items-center gap-2">
            <InstallDialog serverId={serverId} updates={updates} onConfirm={(all) => install.mutate(all)} />
            <PackageList updates={updates} />
          </div>
        )
      )}
      {run && !run.running && run.finishedAt && (
        <Collapsible>
          <p className={cn("flex flex-wrap items-center gap-x-2", run.error ? "text-destructive" : "text-muted-foreground")}>
            {run.error ? `Install failed ${timeAgo(run.finishedAt)}: ${run.error}` : `Updates installed ${timeAgo(run.finishedAt)}.`}
            {run.log && (
              <CollapsibleTrigger asChild>
                <Button variant="link" size="sm" className="group h-auto px-0 text-muted-foreground">
                  Log
                  <ChevronDown data-icon="inline-end" className="transition-transform group-data-[state=open]:rotate-180" />
                </Button>
              </CollapsibleTrigger>
            )}
          </p>
          <CollapsibleContent>
            <pre className={cn(logBox, "mt-2 max-h-72 px-3 whitespace-pre-wrap")}>{run.log}</pre>
          </CollapsibleContent>
        </Collapsible>
      )}
    </div>
  );
}

function RebootButton({ serverId, disabled }: { serverId: string; disabled: boolean }) {
  const qc = useQueryClient();
  const reboot = useMutation({
    meta: { error: "Couldn't reboot the server" },
    mutationFn: () => api.rebootServer(serverId),
    onSuccess: (r) => qc.setQueryData(["server-security", serverId], r),
  });
  return (
    <div className="pt-1.5">
      <ConfirmDialog
        title="Reboot this server?"
        description={
          serverId === "local"
            ? "Every app on it, and this manager, is down for a few minutes. The page reconnects once the manager is back."
            : "Every app on it is down for a few minutes, until the host is back and Docker restarts them."
        }
        confirmLabel="Reboot"
        onConfirm={() => reboot.mutate()}
        trigger={
          <Button size="sm" variant="outline" disabled={disabled || reboot.isPending}>
            {reboot.isPending ? <Spinner data-icon="inline-start" /> : <Power data-icon="inline-start" />}
            Reboot
          </Button>
        }
      />
    </div>
  );
}

function PackageList({ updates }: { updates: PendingUpdate[] }) {
  return (
    <Collapsible className="w-full">
      <CollapsibleTrigger asChild>
        <Button variant="link" size="sm" className="group h-auto px-0 text-muted-foreground">
          {updates.length} pending {updates.length === 1 ? "update" : "updates"}
          <ChevronDown data-icon="inline-end" className="transition-transform group-data-[state=open]:rotate-180" />
        </Button>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <ul className="mt-1 max-h-72 divide-y overflow-auto rounded-lg border text-xs">
          {updates.map((u) => (
            <li key={u.name} className="flex flex-wrap items-baseline gap-x-2 px-3 py-1.5">
              <span className="font-mono">{u.name}</span>
              {u.version && <span className="font-mono text-muted-foreground">{u.version}</span>}
              <span className="ml-auto flex gap-1.5">
                {u.security && <span className="rounded bg-amber-500/15 px-1.5 text-amber-700 dark:text-amber-400">security</span>}
                {u.impact && <span className="rounded bg-muted px-1.5 text-muted-foreground">{impactLabel[u.impact]}</span>}
              </span>
            </li>
          ))}
        </ul>
      </CollapsibleContent>
    </Collapsible>
  );
}

/** Asks before installing, with what the install will restart. */
function InstallDialog({ serverId, updates, onConfirm }: { serverId: string; updates: PendingUpdate[]; onConfirm: (all: boolean) => void }) {
  const security = updates.filter((u) => u.security).length;
  const [all, setAll] = useState(true);
  const picked = all ? updates : updates.filter((u) => u.security);
  const names = (impact: UpdateImpact) =>
    picked
      .filter((u) => u.impact === impact)
      .map((u) => u.name)
      .join(", ");
  const docker = names("docker");
  const reboot = names("reboot");
  const ssh = names("ssh");

  return (
    <AlertDialog onOpenChange={(open) => open && setAll(true)}>
      <AlertDialogTrigger asChild>
        <Button size="sm">
          <Download data-icon="inline-start" />
          Install updates
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            {all
              ? `Install ${updates.length} ${updates.length === 1 ? "update" : "updates"}${security ? ` (${security} security)` : ""}?`
              : `Install ${security} security ${security === 1 ? "update" : "updates"}?`}
          </AlertDialogTitle>
          <AlertDialogDescription>
            The host's package manager installs them in the background, keeping changed config files. What it restarts:
          </AlertDialogDescription>
        </AlertDialogHeader>
        <ul className="list-disc space-y-1 pl-5 text-sm text-muted-foreground">
          {docker && (
            <li className="text-amber-700 dark:text-amber-400">
              Docker restarts ({docker}): every app on this server stops for about a minute
              {serverId === "local" && ", and so does this manager: the page reconnects once it's back"}.
            </li>
          )}
          {reboot && <li>{reboot}: take effect after a reboot.</li>}
          {ssh && <li>The SSH server restarts; open sessions stay.</li>}
          <li>Other services using an updated library restart.</li>
        </ul>
        {security > 0 && security < updates.length && (
          <CheckboxField
            label={`Also install non-security updates (${updates.length - security})`}
            checked={all}
            onCheckedChange={setAll}
          />
        )}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction onClick={() => onConfirm(all)}>Install</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
