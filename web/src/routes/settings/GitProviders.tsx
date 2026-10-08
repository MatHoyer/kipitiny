import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, ExternalLink, GitFork, Pencil, Plus, Trash2, Webhook } from "lucide-react";
import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { useSearchParams } from "react-router";
import { toast } from "sonner";
import { GitHubIcon, GitLabIcon } from "@/components/brand-icons";
import { ChoiceTile, CopyField, EmptyState, ErrorText, Mono, Tag, TestButton } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { api, type GitProvider, type GitProviderInput, type GitProviderKind } from "@/api";
import { SettingsPage } from "./page";

type Kind = {
  id: GitProviderKind;
  label: string;
  icon: ReactNode;
  description: string;
  /** The hosted forge; empty when there is none (self-hosted only). */
  baseUrl: string;
};

const kinds: Kind[] = [
  {
    id: "github",
    label: "GitHub",
    icon: <GitHubIcon />,
    description: "A GitHub App, created in one click and installed on your account or organization.",
    baseUrl: "https://github.com",
  },
  {
    id: "gitlab",
    label: "GitLab",
    icon: <GitLabIcon className="text-[#FC6D26]" />,
    description: "An OAuth application on gitlab.com or your own GitLab.",
    baseUrl: "https://gitlab.com",
  },
  {
    id: "gitea",
    label: "Gitea",
    icon: <GitFork className="text-[#609926]" />,
    description: "An OAuth application on your Gitea or Forgejo.",
    baseUrl: "",
  },
];

const kindOf = (id: GitProviderKind) => kinds.find((k) => k.id === id)!;

export const gitProviderIcon = (kind: GitProviderKind) => kindOf(kind).icon;

/** Where the forge sends the browser back after an OAuth authorization. */
const oauthCallback = () => `${window.location.origin}/api/git-providers/oauth/callback`;

/** Opens the forge page that connects a provider. */
async function connect(id: string) {
  const { url } = await api.authorizeGitProvider(id);
  window.location.assign(url);
}

type Editing = { step: "pick" } | { step: "new"; kind: Kind } | { step: "edit"; provider: GitProvider };

/** Access to private repositories, for projects that follow a compose file in git. */
export function GitProviders() {
  const qc = useQueryClient();
  const providers = useQuery({ queryKey: ["git-providers"], queryFn: api.gitProviders });
  const [editing, setEditing] = useState<Editing | null>(null);
  const [params, setParams] = useSearchParams();

  // The forge sent the browser back here.
  useEffect(() => {
    const error = params.get("error");
    if (!error && !params.get("connected")) return;
    if (error) toast.error("Couldn't connect the git provider", { description: error });
    else toast.success("Git provider connected");
    qc.invalidateQueries({ queryKey: ["git-providers"] });
    setParams({}, { replace: true });
  }, [params, setParams, qc]);

  const add = (
    <Button size="sm" onClick={() => setEditing({ step: "pick" })}>
      <Plus data-icon="inline-start" />
      Add provider
    </Button>
  );

  return (
    <SettingsPage actions={!!providers.data?.length && add}>
      <ErrorText error={providers.error} />
      {providers.data?.length === 0 ? (
        <EmptyState
          icon={GitFork}
          title="No git providers"
          description="Public repositories need none. Connect GitHub, GitLab or Gitea to follow a compose file in a private one, without pasting tokens."
          action={add}
        />
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {providers.data?.map((p) => (
            <ProviderCard key={p.id} provider={p} onEdit={() => setEditing({ step: "edit", provider: p })} />
          ))}
        </div>
      )}
      <Dialog open={editing !== null} onOpenChange={(o) => !o && setEditing(null)}>
        <DialogContent className="sm:max-w-lg">
          {editing?.step === "pick" && (
            <>
              <DialogHeader>
                <DialogTitle>Add a git provider</DialogTitle>
                <DialogDescription>Where are your repositories?</DialogDescription>
              </DialogHeader>
              <div className="grid gap-3 sm:grid-cols-2">
                {kinds.map((k) => (
                  <ChoiceTile key={k.id} icon={k.icon} title={k.label} description={k.description} onClick={() => setEditing({ step: "new", kind: k })} />
                ))}
              </div>
            </>
          )}
          {editing?.step === "new" && editing.kind.id === "github" && <GitHubAppForm onBack={() => setEditing({ step: "pick" })} />}
          {editing?.step === "new" && editing.kind.id !== "github" && (
            <OAuthForm key={editing.kind.id} kind={editing.kind} provider={null} onBack={() => setEditing({ step: "pick" })} onDone={() => setEditing(null)} />
          )}
          {editing?.step === "edit" && editing.provider.kind === "github" && <RenameForm provider={editing.provider} onDone={() => setEditing(null)} />}
          {editing?.step === "edit" && editing.provider.kind !== "github" && (
            <OAuthForm key={editing.provider.id} kind={kindOf(editing.provider.kind)} provider={editing.provider} onDone={() => setEditing(null)} />
          )}
        </DialogContent>
      </Dialog>
    </SettingsPage>
  );
}

function ProviderCard({ provider: p, onEdit }: { provider: GitProvider; onEdit: () => void }) {
  const qc = useQueryClient();
  const kind = kindOf(p.kind);
  const remove = useMutation({
    meta: { error: `Couldn't remove ${p.name}` },
    mutationFn: () => api.deleteGitProvider(p.id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["git-providers"] }),
  });
  const reconnect = useMutation({ meta: { error: `Couldn't connect ${p.name}` }, mutationFn: () => connect(p.id) });
  const status = !p.connected ? "Not connected" : p.kind === "github" ? `Installed on ${p.account}` : `Authorized by ${p.account}`;

  return (
    <Card className="gap-3 px-4">
      <div className="flex items-start gap-3">
        <span className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-muted [&>svg]:size-5">{kind.icon}</span>
        <div className="min-w-0 flex-1">
          <p className="truncate font-medium">{p.name}</p>
          <p className={p.connected ? "truncate text-xs text-muted-foreground" : "truncate text-xs text-destructive"}>{status}</p>
        </div>
        {p.webhooks && (
          <Tag className="gap-1">
            <Webhook className="size-3" />
            Webhooks
          </Tag>
        )}
      </div>
      {p.baseUrl !== kind.baseUrl && <p className="truncate text-xs text-muted-foreground">{p.baseUrl}</p>}
      <div className="mt-auto flex items-center justify-end gap-0.5">
        {p.connected ? (
          <TestButton name={p.name} test={() => api.testGitProvider(p.id)} />
        ) : (
          <Button size="sm" variant="outline" loading={reconnect.isPending} onClick={() => reconnect.mutate()}>
            Connect
          </Button>
        )}
        {p.connected && (
          <Button
            variant="ghost"
            size="sm"
            disabled={reconnect.isPending}
            onClick={() => reconnect.mutate()}
            title={p.kind === "github" ? "Choose which repositories the app can read" : "Authorize again"}
          >
            {p.kind === "github" ? "Repositories" : "Reconnect"}
          </Button>
        )}
        <Button variant="ghost" size="icon-sm" title="Edit" aria-label="Edit" onClick={onEdit}>
          <Pencil />
        </Button>
        <ConfirmDialog
          trigger={
            <Button variant="ghost" size="icon-sm" title="Remove" aria-label="Remove" className="text-muted-foreground hover:text-destructive">
              <Trash2 />
            </Button>
          }
          title={`Remove ${p.name}?`}
          description={
            p.kind === "github" ? (
              <>
                kipitiny forgets the app. Delete it on GitHub too, from the app's settings (<Mono>{p.appSlug}</Mono>).
              </>
            ) : (
              "kipitiny forgets the authorization. Revoke it on the forge too, under the account's authorized applications."
            )
          }
          confirmLabel="Remove"
          onConfirm={() => remove.mutate()}
        />
      </div>
    </Card>
  );
}

/** Creates a GitHub App from a manifest: the browser posts it to GitHub, which sends it back to install the app. */
function GitHubAppForm({ onBack }: { onBack: () => void }) {
  const [form, setForm] = useState({ name: "GitHub", org: "", baseUrl: "" });
  const [enterprise, setEnterprise] = useState(false);
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });
  const start = useMutation({
    meta: { error: "Couldn't start creating the GitHub App" },
    mutationFn: () => api.startGitHubApp(form),
    // The manager's page posts the manifest: GitHub takes it as a form post,
    // which this page's CSP doesn't allow to another origin.
    onSuccess: ({ url }) => window.location.assign(url),
  });
  // As the manager decides whether the app gets webhooks.
  const local = /^(localhost|127\.|10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.)|\.(local|localhost|internal|lan)$|^[^.]+$/.test(window.location.hostname);
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    start.mutate();
  };

  return (
    <form onSubmit={onSubmit} className="contents">
      <DialogHeader>
        <DialogTitle className="flex items-center gap-2 [&>svg]:size-5">
          <GitHubIcon />
          Connect GitHub
        </DialogTitle>
        <DialogDescription>
          kipitiny creates a private GitHub App that can only read repository contents. On GitHub, confirm its creation, then choose the repositories
          it may read.
        </DialogDescription>
      </DialogHeader>
      <div className="space-y-4">
        <FloatingInput label="Name in kipitiny" required autoFocus value={form.name} onChange={set("name")} />
        <FloatingInput
          label="Organization (optional)"
          value={form.org}
          onChange={set("org")}
          placeholder="acme"
          description="Create the app under an organization you own; empty for your personal account."
        />
        {enterprise ? (
          <FloatingInput label="GitHub Enterprise Server" value={form.baseUrl} onChange={set("baseUrl")} placeholder="https://github.example.com" />
        ) : (
          <button type="button" className="text-xs text-muted-foreground underline underline-offset-2" onClick={() => setEnterprise(true)}>
            Using GitHub Enterprise Server?
          </button>
        )}
        {local && (
          <p className="text-xs text-amber-600 dark:text-amber-400">
            GitHub can't reach {window.location.host}, so the app gets no webhooks: projects pick up pushes by polling.
          </p>
        )}
      </div>
      <DialogFooter>
        <Button type="button" variant="ghost" disabled={start.isPending} onClick={onBack}>
          <ChevronLeft data-icon="inline-start" />
          Back
        </Button>
        <Button type="submit" loading={start.isPending}>
          <ExternalLink data-icon="inline-start" />
          Continue on GitHub
        </Button>
      </DialogFooter>
    </form>
  );
}

function RenameForm({ provider, onDone }: { provider: GitProvider; onDone: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState(provider.name);
  const save = useMutation({
    meta: { error: "Couldn't rename the provider" },
    mutationFn: () => api.updateGitProvider(provider.id, { name, baseUrl: "", clientId: "", clientSecret: "" }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["git-providers"] });
      onDone();
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <form onSubmit={onSubmit} className="contents">
      <DialogHeader>
        <DialogTitle>Rename {provider.name}</DialogTitle>
      </DialogHeader>
      <FloatingInput label="Name" required autoFocus value={name} onChange={(e) => setName(e.target.value)} />
      <DialogFooter>
        <Button type="submit" loading={save.isPending}>
          Save
        </Button>
      </DialogFooter>
    </form>
  );
}

/** A GitLab or Gitea OAuth application: registered on the forge, then authorized. */
function OAuthForm({ kind, provider, onBack, onDone }: { kind: Kind; provider: GitProvider | null; onBack?: () => void; onDone: () => void }) {
  const qc = useQueryClient();
  const [form, setForm] = useState<GitProviderInput>(
    provider
      ? { name: provider.name, baseUrl: provider.baseUrl, clientId: provider.clientId ?? "", clientSecret: provider.clientSecret ?? "" }
      : { kind: kind.id, name: kind.label, baseUrl: kind.baseUrl, clientId: "", clientSecret: "" },
  );
  const set = (k: keyof GitProviderInput) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });
  const save = useMutation({
    meta: { error: "Couldn't save the provider" },
    mutationFn: async () => {
      const p = provider ? await api.updateGitProvider(provider.id, form) : await api.createGitProvider(form);
      qc.invalidateQueries({ queryKey: ["git-providers"] });
      // A new (or changed) application is authorized right away.
      if (!p.connected) await connect(p.id);
      else onDone();
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };
  const base = (form.baseUrl || kind.baseUrl || "https://git.example.com").replace(/\/+$/, "");
  const where =
    kind.id === "gitlab" ? (
      <>
        <a className="underline underline-offset-2" href={`${base}/-/user_settings/applications`} target="_blank" rel="noreferrer">
          User settings › Applications
        </a>{" "}
        (or a group's), with the scopes <Mono>read_api</Mono> and <Mono>read_repository</Mono>
      </>
    ) : (
      <>
        <a className="underline underline-offset-2" href={`${base}/user/settings/applications`} target="_blank" rel="noreferrer">
          Settings › Applications
        </a>
      </>
    );

  return (
    <form onSubmit={onSubmit} className="contents">
      <DialogHeader>
        <DialogTitle className="flex items-center gap-2 [&>svg]:size-5">
          {kind.icon}
          {provider ? `Edit ${provider.name}` : `Connect ${kind.label}`}
        </DialogTitle>
        <DialogDescription>
          Create an OAuth application in {where}, redirecting to the URI below. Paste its ID and secret: you then authorize it on {kind.label}.
        </DialogDescription>
      </DialogHeader>
      <div className="space-y-4">
        <CopyField value={oauthCallback()} />
        <FloatingInput label="Name in kipitiny" required value={form.name} onChange={set("name")} />
        <FloatingInput
          label={`${kind.label} server`}
          required={!kind.baseUrl}
          value={form.baseUrl}
          onChange={set("baseUrl")}
          placeholder={kind.baseUrl || "https://git.example.com"}
          description={kind.baseUrl ? "Change it for a self-hosted GitLab." : undefined}
        />
        <FloatingInput label={kind.id === "gitlab" ? "Application ID" : "Client ID"} required value={form.clientId} onChange={set("clientId")} autoComplete="off" />
        <FloatingInput
          label={kind.id === "gitlab" ? "Secret" : "Client secret"}
          required
          type="password"
          value={form.clientSecret}
          onChange={set("clientSecret")}
          autoComplete="new-password"
          description={provider ? "Leave as is to keep the saved one." : undefined}
        />
      </div>
      <DialogFooter>
        {onBack && (
          <Button type="button" variant="ghost" disabled={save.isPending} onClick={onBack}>
            <ChevronLeft data-icon="inline-start" />
            Back
          </Button>
        )}
        <Button type="submit" loading={save.isPending}>
          {provider ? "Save" : `Authorize on ${kind.label}`}
        </Button>
      </DialogFooter>
    </form>
  );
}
