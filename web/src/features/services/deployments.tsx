import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, GitCommitHorizontal, RefreshCw, Rocket, Workflow } from "lucide-react";
import { useEffect, useState } from "react";
import { Mono, statusIcon, Tag, triggeredBy } from "@/components/common";
import { CopyButton } from "@kipit-labs/kipitidy/copy-button";
import { EmptyState } from "@kipit-labs/kipitidy/empty-state";
import { ConfirmDialog } from "@kipit-labs/kipitidy/confirm-dialog";
import { Button } from "@kipit-labs/kipitidy/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@kipit-labs/kipitidy/collapsible";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from "@kipit-labs/kipitidy/dialog";
import { DeploymentLogView } from "@/features/services/logs";
import { byDay, formatDuration, timeAgo } from "@/lib/format";
import { cn } from "@/lib/utils";
import { api, type Deployment, type Service as ServiceT } from "../../api";

export function Deployments({
  svc,
  deployments,
  focus,
  onFocus,
  busy,
}: {
  svc: ServiceT;
  deployments: Deployment[];
  /** Opened when it changes, e.g. a deploy just started. */
  focus: string | null;
  onFocus: (id: string) => void;
  busy: boolean;
}) {
  const qc = useQueryClient();
  const [open, setOpen] = useState<Set<string>>(() => {
    const live = deployments[0]?.status === "running" ? deployments[0].id : null;
    return new Set([focus, live].filter((x) => x !== null));
  });
  useEffect(() => {
    if (focus) setOpen((s) => new Set(s).add(focus));
  }, [focus]);
  const toggle = (id: string, on: boolean) =>
    setOpen((s) => {
      const next = new Set(s);
      if (on) next.add(id);
      else next.delete(id);
      return next;
    });

  const rollback = useMutation({
    meta: { error: "Couldn't roll back" },
    mutationFn: (deploymentId: string) => api.rollback(svc.id, deploymentId),
    onSuccess: (d) => {
      onFocus(d.id);
      qc.invalidateQueries({ queryKey: ["deployments", svc.id] });
    },
  });

  const ci = svc.kind === "app" && <DeployFromCIDialog svc={svc} />;
  if (deployments.length === 0)
    return <EmptyState icon={Rocket} title="No deployments yet" description="Deploy the service to see its builds and logs here." action={ci} />;

  return (
    <div className="space-y-6">
      {ci && <div className="flex justify-end">{ci}</div>}
      {byDay(deployments).map(([day, rows]) => (
        <section key={day} className="space-y-2">
          <h3 className="text-xs font-medium tracking-wide text-muted-foreground uppercase">{day}</h3>
          <ul className="divide-y overflow-hidden rounded-xl border">
            {rows.map((d) => (
              <DeploymentRow
                key={d.id}
                deployment={d}
                current={d.id === svc.currentDeploymentId}
                open={open.has(d.id)}
                onOpenChange={(on) => toggle(d.id, on)}
                onRollback={
                  svc.kind === "app" && d.status === "succeeded" && d.id !== svc.currentDeploymentId && !busy
                    ? () => rollback.mutate(d.id)
                    : undefined
                }
              />
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

function DeploymentRow({
  deployment: d,
  current,
  open,
  onOpenChange,
  onRollback,
}: {
  deployment: Deployment;
  current: boolean;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onRollback?: () => void;
}) {
  const end = d.finishedAt ? new Date(d.finishedAt).getTime() : Date.now();
  const took = formatDuration(Math.max(0, end - new Date(d.createdAt).getTime()));
  return (
    <Collapsible asChild open={open} onOpenChange={onOpenChange}>
      <li className={cn(open && "bg-muted/30")}>
        <div className="flex items-center gap-2 pr-3">
          <CollapsibleTrigger className="group flex min-w-0 flex-1 items-center gap-3 py-3 pl-3 text-left outline-none focus-visible:bg-muted/50">
            <ChevronRight className="size-4 shrink-0 text-muted-foreground transition-transform group-data-[state=open]:rotate-90" />
            <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted">{statusIcon[d.status]}</span>
            <div className="min-w-0 flex-1">
              <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
                {d.gitCommit ? (
                  <span className="inline-flex items-center gap-1.5 font-mono">
                    <GitCommitHorizontal className="size-4 text-muted-foreground" />
                    {d.gitCommit.slice(0, 7)}
                  </span>
                ) : (
                  <span className="truncate font-mono" title={d.image}>
                    {shortImage(d.image)}
                  </span>
                )}
                {current && <Tag className="bg-emerald-500/10 text-emerald-700 dark:text-emerald-400">current</Tag>}
              </p>
              <p className="truncate text-xs text-muted-foreground">
                {new Date(d.createdAt).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })} ·{" "}
                {d.status === "running" ? `running for ${took}` : took}
                {d.triggeredBy && <> · by {triggeredBy(d.triggeredBy)}</>}
                {d.error && <span className={d.status === "skipped" ? "text-pink-600 dark:text-pink-400" : "text-destructive"}> · {d.error}</span>}
              </p>
            </div>
            <span className="text-xs whitespace-nowrap text-muted-foreground max-sm:hidden">{timeAgo(d.createdAt)}</span>
          </CollapsibleTrigger>
          {onRollback && (
            <ConfirmDialog
              trigger={
                <Button variant="outline" size="sm">
                  <RefreshCw data-icon="inline-start" />
                  Roll back
                </Button>
              }
              title="Roll back?"
              description={`Redeploys ${d.image}. Current settings are kept; the next regular deploy uses the image in Settings again.`}
              confirmLabel="Roll back"
              destructive={false}
              onConfirm={onRollback}
            />
          )}
        </div>
        <CollapsibleContent>
          <DeploymentLog deployment={d} />
        </CollapsibleContent>
      </li>
    </Collapsible>
  );
}

const terminal = "overflow-auto rounded-lg bg-neutral-950 p-3 font-mono text-xs leading-relaxed text-neutral-200 dark:ring-1 dark:ring-foreground/10";

function DeploymentLog({ deployment }: { deployment: Deployment }) {
  const log = useQuery({
    queryKey: ["deployment-log", deployment.id],
    queryFn: () => api.deploymentLog(deployment.id),
    refetchInterval: deployment.status === "running" ? 1_000 : false,
  });
  return (
    <div className="space-y-2 px-3 pb-3">
      {deployment.image && deployment.gitCommit && (
        <p className="truncate font-mono text-xs text-muted-foreground">{deployment.image}</p>
      )}
      {deployment.error && <p className="text-sm text-destructive">{deployment.error}</p>}
      <DeploymentLogView text={log.data} pending={log.isPending} className="max-h-96" />
    </div>
  );
}

/** The CI step that deploys the tag it just pushed. */
function DeployFromCIDialog({ svc }: { svc: ServiceT }) {
  const repo = imageRepo(svc.image);
  const status = useQuery({ queryKey: ["status"], queryFn: api.status });
  // The action of this manager's release; a dev build has none, so main.
  const version = status.data?.version ?? "";
  const ref = /^\d+\.\d+\.\d+$/.test(version) ? version : "main";
  const step = [
    "- name: Deploy",
    `  uses: kipit-labs/kipitiny/deploy@${ref}`,
    "  with:",
    `    url: ${window.location.origin}`,
    "    token: ${{ secrets.KIPITINY_TOKEN }}",
    `    service: ${svc.id}`,
    "    tag: sha-${{ github.sha }}",
  ].join("\n");
  const dockerStep = [
    "- name: Deploy",
    "  run: >",
    "    docker run --rm -e KIPITINY_TOKEN=${{ secrets.KIPITINY_TOKEN }}",
    "    ghcr.io/kipit-labs/kipitiny deploy",
    `    --url ${window.location.origin} --service ${svc.id}`,
    "    --tag sha-${{ github.sha }} --commit ${{ github.sha }}",
  ].join("\n");
  return (
    <Dialog>
      <DialogTrigger asChild>
        <Button variant="outline" size="sm">
          <Workflow data-icon="inline-start" />
          Deploy from CI
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>Deploy from CI</DialogTitle>
          <DialogDescription>
            Build and push <Mono>{repo}</Mono> in CI, then deploy the tag it pushed with a <Mono>deploy</Mono> token (Access › API
            tokens & MCP). The service keeps that tag; the step fails if the deployment does.
          </DialogDescription>
        </DialogHeader>
        <CopyBlock value={step} />
        <p className="text-muted-foreground text-sm">Other CI systems run the same command from the manager image:</p>
        <CopyBlock value={dockerStep} />
      </DialogContent>
    </Dialog>
  );
}

function CopyBlock({ value }: { value: string }) {
  return (
    <div className="relative">
      <pre className={cn(terminal, "pr-10")}>{value}</pre>
      <div className="absolute top-1.5 right-1.5 text-neutral-200">
        <CopyButton value={value} label="Copy step" />
      </div>
    </div>
  );
}

/** The image without its tag or digest. */
function imageRepo(image: string) {
  const name = image.split("@")[0];
  const colon = name.lastIndexOf(":");
  return colon > name.lastIndexOf("/") ? name.slice(0, colon) : name;
}

/** The image without its pinned digest: name:tag stays readable. */
function shortImage(image: string) {
  return image.split("@")[0];
}
