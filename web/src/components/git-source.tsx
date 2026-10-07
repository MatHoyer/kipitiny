import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { GitBranch, Link2Off, RefreshCw, ScanSearch } from "lucide-react";
import { useState, type FormEvent } from "react";
import { toast } from "sonner";
import { CheckboxField, CopyButton, ErrorText, Loading, Mono, Section, Tag } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { PlanView } from "@/components/compose-dialog";
import { Button } from "@/components/ui/button";
import { FloatingInput } from "@/components/ui/floating-input";
import { formatDateTime, timeAgo } from "@/lib/format";
import { DOCS_URL } from "@/lib/docs";
import { api, type ComposePlan, type GitInput, type GitStatus } from "../api";

/** The project's git link, or null; undefined while loading. */
export function useProjectGit(projectId: string) {
  return useQuery({
    queryKey: ["git", projectId],
    queryFn: () => api.projectGit(projectId),
    enabled: !!projectId,
    refetchInterval: 15_000,
  });
}

/** Links a project to a compose file in a git repository, and shows the sync. */
export function GitSource({ projectId }: { projectId: string }) {
  const git = useProjectGit(projectId);
  const [editing, setEditing] = useState(false);
  if (git.error) return <ErrorText error={git.error} />;
  if (git.data === undefined) return <Loading />;
  if (!git.data || editing)
    return <GitForm projectId={projectId} current={git.data ?? undefined} onDone={() => setEditing(false)} />;
  return <GitStatusCard projectId={projectId} git={git.data} onEdit={() => setEditing(true)} />;
}

const emptyInput: GitInput = { repoUrl: "", branch: "main", path: "compose.yaml", token: "", autoSync: true, pollSeconds: 300 };

function GitForm({ projectId, current, onDone }: { projectId: string; current?: GitStatus; onDone: () => void }) {
  const qc = useQueryClient();
  const [input, setInput] = useState<GitInput>(current ? { ...current } : emptyInput);
  const [plan, setPlan] = useState<ComposePlan | null>(null);
  const set = (patch: Partial<GitInput>) => {
    setInput({ ...input, ...patch });
    setPlan(null);
  };
  const preview = useMutation({ mutationFn: () => api.previewProjectGit(projectId, input), onSuccess: setPlan });
  const link = useMutation({
    mutationFn: () => api.linkProjectGit(projectId, input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["git", projectId] });
      qc.invalidateQueries({ queryKey: ["services", projectId] });
      toast.success("Linked; syncing now");
      onDone();
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (plan) link.mutate();
    else preview.mutate();
  };
  return (
    <Section
      title={current ? "Edit the git link" : "Follow a git repository"}
      description={
        <>
          The project's services follow a compose file in the repository: created, updated and (apps only) deleted to match it on every
          push. Services can then only change through the file; CI can still deploy another image tag. The file's format is in the{" "}
          <a href={`${DOCS_URL}/compose`} target="_blank" rel="noreferrer" className="underline underline-offset-2">
            compose reference
          </a>
          .
        </>
      }
    >
      <form onSubmit={onSubmit} className="space-y-4">
        <div className="grid gap-4 sm:grid-cols-2">
          <FloatingInput
            label="Repository (https)"
            required
            className="sm:col-span-2"
            value={input.repoUrl}
            onChange={(e) => set({ repoUrl: e.target.value })}
            placeholder="https://github.com/me/infra.git"
          />
          <FloatingInput label="Branch" value={input.branch} onChange={(e) => set({ branch: e.target.value })} placeholder="main" />
          <FloatingInput label="Compose file" value={input.path} onChange={(e) => set({ path: e.target.value })} placeholder="compose.yaml" />
          <FloatingInput
            label="Access token"
            type="password"
            autoComplete="off"
            value={input.token}
            onChange={(e) => set({ token: e.target.value })}
            description="Read access to the repository's contents; empty for a public one."
          />
          <FloatingInput
            label="Poll every (seconds)"
            type="number"
            min={60}
            value={String(input.pollSeconds)}
            onChange={(e) => set({ pollSeconds: Number(e.target.value) || 0 })}
            disabled={!input.autoSync}
          />
        </div>
        <CheckboxField
          label="Sync automatically"
          description="Poll the branch; a push webhook syncs right away either way."
          checked={input.autoSync}
          onCheckedChange={(v) => set({ autoSync: v })}
        />
        {plan && <PlanView plan={plan} />}
        {plan && plan.delete.length > 0 && (
          <p className="text-sm text-destructive">The apps to delete are not in the file: they and their volumes go away.</p>
        )}
        <ErrorText error={preview.error ?? link.error} />
        <div className="flex justify-end gap-2">
          {current && (
            <Button type="button" variant="ghost" onClick={onDone}>
              Cancel
            </Button>
          )}
          {plan ? (
            <Button type="submit" disabled={link.isPending}>
              <GitBranch data-icon="inline-start" />
              {current ? "Save and sync" : "Link and sync"}
            </Button>
          ) : (
            <Button type="submit" disabled={!input.repoUrl.trim() || preview.isPending}>
              <ScanSearch data-icon="inline-start" />
              Preview changes
            </Button>
          )}
        </div>
      </form>
    </Section>
  );
}

function GitStatusCard({ projectId, git, onEdit }: { projectId: string; git: GitStatus; onEdit: () => void }) {
  const qc = useQueryClient();
  const services = useQuery({ queryKey: ["services", projectId], queryFn: () => api.services(projectId) });
  const orphaned = services.data?.filter((s) => s.orphaned) ?? [];
  const [plan, setPlan] = useState<ComposePlan | null>(null);
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["git", projectId] });
    qc.invalidateQueries({ queryKey: ["services", projectId] });
  };
  const preview = useMutation({ mutationFn: () => api.syncProjectGit(projectId, true), onSuccess: setPlan });
  const sync = useMutation({
    mutationFn: () => api.syncProjectGit(projectId),
    onSuccess: () => {
      setPlan(null);
      refresh();
      toast.success("Synced");
    },
    onError: refresh,
  });
  const unlink = useMutation({
    mutationFn: () => api.unlinkProjectGit(projectId),
    onSuccess: refresh,
  });
  const webhook = `${window.location.origin}${git.webhookPath}`;
  return (
    <Section
      title="Git"
      description={
        <>
          Following <Mono>{git.path}</Mono> on <Mono>{git.branch}</Mono> of {git.repoUrl}
          {git.autoSync ? `, checked every ${git.pollSeconds} s.` : ", synced on push and by hand."}
        </>
      }
      actions={
        <>
          <Button size="sm" variant="outline" disabled={preview.isPending} onClick={() => preview.mutate()}>
            <ScanSearch data-icon="inline-start" />
            Preview
          </Button>
          <Button size="sm" disabled={sync.isPending} onClick={() => sync.mutate()}>
            <RefreshCw data-icon="inline-start" />
            Sync now
          </Button>
        </>
      }
    >
      <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-[10rem_1fr]">
        <dt className="text-muted-foreground">Last commit</dt>
        <dd>{git.lastCommit ? <Mono>{git.lastCommit.slice(0, 12)}</Mono> : "not synced yet"}</dd>
        <dt className="text-muted-foreground">Last sync</dt>
        <dd title={git.lastSyncedAt && formatDateTime(git.lastSyncedAt)}>{git.lastSyncedAt ? timeAgo(git.lastSyncedAt) : "never"}</dd>
        <dt className="text-muted-foreground">Push webhook</dt>
        <dd className="flex min-w-0 items-center gap-1">
          <Mono>{webhook}</Mono>
          <CopyButton value={webhook} label="Copy URL" />
        </dd>
        <dt className="text-muted-foreground">Webhook secret</dt>
        <dd className="flex items-center gap-1">
          <Mono>••••••••</Mono>
          <CopyButton value={git.webhookSecret} label="Copy secret" />
          <span className="text-xs text-muted-foreground">GitHub/Gitea secret, GitLab token, or Bearer token.</span>
        </dd>
        {git.drift.length > 0 && (
          <>
            <dt className="text-muted-foreground">Other image than the file</dt>
            <dd className="flex flex-wrap gap-1">
              {git.drift.map((n) => (
                <Tag key={n}>
                  {n}: {git.applied[n]}
                </Tag>
              ))}
            </dd>
          </>
        )}
        {orphaned.length > 0 && (
          <>
            <dt className="text-muted-foreground">No longer in the file</dt>
            <dd className="flex flex-wrap items-center gap-1">
              {orphaned.map((s) => (
                <Tag key={s.id}>{s.name}</Tag>
              ))}
              <span className="text-xs text-muted-foreground">Databases are kept; delete them from their page.</span>
            </dd>
          </>
        )}
      </dl>
      {git.lastError && (
        <p role="alert" className="text-sm text-destructive">
          {git.lastError}
        </p>
      )}
      {git.warnings.length > 0 && (
        <ul className="list-disc space-y-0.5 pl-5 text-xs text-amber-600 dark:text-amber-400">
          {git.warnings.map((w) => (
            <li key={w}>{w}</li>
          ))}
        </ul>
      )}
      {plan && <PlanView plan={plan} />}
      <ErrorText error={preview.error ?? sync.error ?? unlink.error} />
      <div className="flex justify-end gap-2">
        <Button size="sm" variant="ghost" onClick={onEdit}>
          Edit
        </Button>
        <ConfirmDialog
          trigger={
            <Button size="sm" variant="outline">
              <Link2Off data-icon="inline-start" />
              Unlink
            </Button>
          }
          title="Stop following the repository?"
          description="The services stay as they are and can be edited again."
          confirmLabel="Unlink"
          onConfirm={() => unlink.mutate()}
        />
      </div>
    </Section>
  );
}
