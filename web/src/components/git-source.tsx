import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { GitBranch, Link2Off, RefreshCw, ScanSearch } from "lucide-react";
import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { toast } from "sonner";
import { CheckboxField, CopyButton, ErrorText, Loading, Mono, Section, Tag } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { PlanView } from "@/components/compose-dialog";
import { Button } from "@/components/ui/button";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingSelect } from "@/components/ui/floating-select";
import { formatDateTime, timeAgo } from "@/lib/format";
import { useDocsUrl } from "@/lib/docs";
import { gitProviderIcon } from "@/routes/settings/GitProviders";
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

const emptyInput: GitInput = { repoUrl: "", branch: "main", path: "compose.yaml", providerId: "", autoSync: true, pollSeconds: 300 };

/** The select's value for "no provider" (a select item can't be empty). */
const publicRepo = "public";

const sameRepo = (a: string, b: string) => {
  const norm = (s: string) => s.trim().toLowerCase().replace(/\/+$/, "").replace(/\.git$/, "");
  return !!a && norm(a) === norm(b);
};

const useGitProviders = () => useQuery({ queryKey: ["git-providers"], queryFn: api.gitProviders });

function GitForm({ projectId, current, onDone }: { projectId: string; current?: GitStatus; onDone: () => void }) {
  const qc = useQueryClient();
  const docs = useDocsUrl();
  const [input, setInput] = useState<GitInput>(current ? { ...current, providerId: current.providerId ?? "" } : emptyInput);
  const [plan, setPlan] = useState<ComposePlan | null>(null);
  const set = (patch: Partial<GitInput>) => {
    setInput({ ...input, ...patch });
    setPlan(null);
  };
  const providers = useGitProviders();
  const usable = providers.data?.filter((p) => p.connected || p.id === input.providerId) ?? [];
  const repos = useQuery({
    queryKey: ["git-repos", input.providerId],
    queryFn: () => api.gitProviderRepos(input.providerId),
    enabled: !!input.providerId,
    staleTime: 60_000,
  });
  const repo = repos.data?.find((r) => sameRepo(r.cloneUrl, input.repoUrl));
  const branches = useQuery({
    queryKey: ["git-branches", input.providerId, repo?.fullName],
    queryFn: () => api.gitProviderBranches(input.providerId, repo!.fullName),
    enabled: !!repo,
    staleTime: 60_000,
  });
  const repoOptions = (repos.data ?? []).map((r) => ({ value: r.cloneUrl, label: r.fullName }));
  if (input.repoUrl && !repo) repoOptions.unshift({ value: input.repoUrl, label: input.repoUrl });
  const branchOptions = [...new Set([input.branch, ...(branches.data ?? [])])].filter(Boolean).map((b) => ({ value: b, label: b }));
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
          <a href={`${docs}/compose`} target="_blank" rel="noreferrer" className="underline underline-offset-2">
            compose reference
          </a>
          .
        </>
      }
    >
      <form onSubmit={onSubmit} className="space-y-4">
        <div className="grid gap-4 sm:grid-cols-2">
          <FloatingSelect
            label="Access"
            className="sm:col-span-2"
            value={input.providerId || publicRepo}
            onValueChange={(v) => set({ providerId: v === publicRepo ? "" : v, repoUrl: "", branch: "main" })}
            loading={providers.isLoading}
            options={[
              { value: publicRepo, label: "Public repository" },
              ...usable.map((p) => ({
                value: p.id,
                label: (
                  <span className="flex items-center gap-2 [&>svg]:size-4">
                    {gitProviderIcon(p.kind)}
                    {p.name}
                    {p.account && <span className="text-muted-foreground">{p.account}</span>}
                  </span>
                ),
              })),
            ]}
            description={
              <>
                A private repository is read through a{" "}
                <Link to="/settings/git-providers" className="underline underline-offset-2">
                  git provider
                </Link>
                .
              </>
            }
          />
          {input.providerId ? (
            <FloatingSelect
              label="Repository"
              className="sm:col-span-2"
              value={input.repoUrl}
              onValueChange={(v) => set({ repoUrl: v, branch: repos.data?.find((r) => r.cloneUrl === v)?.defaultBranch || "main" })}
              loading={repos.isLoading}
              options={repoOptions}
              description={repos.error ? <span className="text-destructive">{repos.error.message}</span> : undefined}
            />
          ) : (
            <FloatingInput
              label="Repository (https)"
              required
              className="sm:col-span-2"
              value={input.repoUrl}
              onChange={(e) => set({ repoUrl: e.target.value })}
              placeholder="https://github.com/me/infra.git"
            />
          )}
          {input.providerId && repo ? (
            <FloatingSelect
              label="Branch"
              value={input.branch}
              onValueChange={(v) => set({ branch: v })}
              loading={branches.isLoading}
              options={branchOptions}
            />
          ) : (
            <FloatingInput label="Branch" value={input.branch} onChange={(e) => set({ branch: e.target.value })} placeholder="main" />
          )}
          <FloatingInput label="Compose file" value={input.path} onChange={(e) => set({ path: e.target.value })} placeholder="compose.yaml" />
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
  const providers = useGitProviders();
  const provider = providers.data?.find((p) => p.id === git.providerId);
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
        <dt className="text-muted-foreground">Access</dt>
        <dd className="flex items-center gap-1.5 [&>svg]:size-4">
          {git.providerId ? (
            provider ? (
              <>
                {gitProviderIcon(provider.kind)}
                {provider.name}
                {!provider.connected && <span className="text-destructive">(not connected)</span>}
              </>
            ) : (
              "a git provider"
            )
          ) : (
            "public repository"
          )}
        </dd>
        <dt className="text-muted-foreground">Push webhook</dt>
        {provider?.webhooks ? (
          <dd>Delivered by the GitHub App, nothing to set up.</dd>
        ) : (
          <>
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
          </>
        )}
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
