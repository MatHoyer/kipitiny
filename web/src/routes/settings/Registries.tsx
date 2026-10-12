import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, Package, Pencil, Plus, Trash2 } from "lucide-react";
import { useState, type FormEvent, type ReactNode } from "react";
import { DockerIcon, GitHubIcon, GitLabIcon } from "@/components/brand-icons";
import { ChoiceTile, EmptyState, ErrorText, Mono } from "@/components/common";
import { HowTo, type HowToStep } from "@/components/how-to";
import { TestDialog } from "@/components/test-dialog";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { api, type Registry, type RegistryInput } from "@/api";
import { SettingsPage } from "./page";

/** Well-known registries; "other" takes any host. */
type Provider = {
  id: string;
  label: string;
  host: string;
  icon: ReactNode;
  description: string;
  username: string;
  password: string;
  help: ReactNode;
  guide?: HowToStep[];
};

const providers: Provider[] = [
  {
    id: "dockerhub",
    label: "Docker Hub",
    host: "docker.io",
    icon: <DockerIcon className="text-[#2496ED]" />,
    description: "Private repositories on hub.docker.com.",
    username: "Docker ID",
    password: "Access token",
    help: "Create a read-only personal access token in Docker Hub › Account settings › Personal access tokens.",
    guide: [
      {
        title: "Generate a token",
        text: "In your account settings, Personal access tokens › Generate new token.",
        link: { href: "https://app.docker.com/settings/personal-access-tokens", label: "Open access tokens" },
      },
      { title: "Make it read-only", text: "Access permissions: Read-only is all pulling needs." },
      { title: "Copy it", text: "Docker Hub shows it once. The Docker ID is your username." },
    ],
  },
  {
    id: "ghcr",
    label: "GitHub",
    host: "ghcr.io",
    icon: <GitHubIcon />,
    description: "GitHub Container Registry (ghcr.io).",
    username: "GitHub username",
    password: "Personal access token",
    help: (
      <>
        A personal access token (classic) with the <Mono>read:packages</Mono> scope.
      </>
    ),
    guide: [
      {
        title: "Create a classic token",
        text: (
          <>
            This link prefills the <Mono>read:packages</Mono> scope; fine-grained tokens can't read packages yet.
          </>
        ),
        link: { href: "https://github.com/settings/tokens/new?scopes=read:packages&description=kipitiny", label: "Create a token" },
      },
      { title: "Pick an expiration", text: "When it expires, pulls fail until you paste a new one here." },
      { title: "Copy it", text: "GitHub shows it once. The username is your GitHub login." },
    ],
  },
  {
    id: "gitlab",
    label: "GitLab",
    host: "registry.gitlab.com",
    icon: <GitLabIcon className="text-[#FC6D26]" />,
    description: "GitLab.com's container registry.",
    username: "Username",
    password: "Token",
    help: (
      <>
        A deploy token or personal access token with <Mono>read_registry</Mono>. Self-hosted GitLab: use Other.
      </>
    ),
    guide: [
      {
        title: "Create a deploy token",
        text: (
          <>
            In the project (or group): Settings › Repository › Deploy tokens, with the <Mono>read_registry</Mono> scope. It only reaches that
            project's images.
          </>
        ),
      },
      {
        title: "Or a personal token",
        text: (
          <>
            To pull from several projects: a personal access token with <Mono>read_registry</Mono>.
          </>
        ),
        link: { href: "https://gitlab.com/-/user_settings/personal_access_tokens?name=kipitiny&scopes=read_registry", label: "Create a personal token" },
      },
      { title: "Copy the username and token", text: "A deploy token comes with its own username, like gitlab+deploy-token-1." },
    ],
  },
  {
    id: "other",
    label: "Other",
    host: "",
    icon: <Package />,
    description: "Any registry: Quay, a cloud registry, your own.",
    username: "Username",
    password: "Password or token",
    help: "Credentials as you'd give them to docker login.",
  },
];

const providerFor = (host: string) => providers.find((p) => p.host === host) ?? providers[providers.length - 1];

type Editing = { step: "pick" } | { step: "new"; provider: Provider } | { step: "edit"; registry: Registry };

/** Credentials for pulling images from private registries. */
export function Registries() {
  const qc = useQueryClient();
  const registries = useQuery({ queryKey: ["registries"], queryFn: api.registries });
  const [editing, setEditing] = useState<Editing | null>(null);
  const remove = useMutation({
    meta: { error: "Couldn't remove the registry" },
    mutationFn: api.deleteRegistry,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["registries"] }),
  });
  const add = (
    <Button size="sm" onClick={() => setEditing({ step: "pick" })}>
      <Plus data-icon="inline-start" />
      Add registry
    </Button>
  );

  return (
    <SettingsPage actions={!!registries.data?.length && add}>
      <ErrorText error={registries.error} />
      {registries.data?.length === 0 ? (
        <EmptyState
          icon={Package}
          title="No private registries"
          description="Public images pull as they are. Add a registry's credentials to deploy images from a private one."
          action={add}
        />
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {registries.data?.map((r) => {
            const p = providerFor(r.host);
            return (
              <Card key={r.id} className="gap-3 px-4">
                <div className="flex items-start gap-3">
                  <span className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-muted [&>svg]:size-5">{p.icon}</span>
                  <div className="min-w-0 flex-1">
                    <p className="truncate font-medium">{p.id === "other" ? r.host : p.label}</p>
                    <p className="truncate text-xs text-muted-foreground">as {r.username}</p>
                  </div>
                </div>
                <p className="truncate text-xs text-muted-foreground">
                  Used for <Mono>{r.host === "docker.io" ? "org/image" : `${r.host}/…`}</Mono>
                </p>
                <div className="mt-auto flex justify-end gap-0.5">
                  <TestDialog
                    name={r.host}
                    icon={p.icon}
                    description="Signs in again with the saved credentials, as a deploy pulling an image would."
                    plan={[{ id: "login", title: `Sign in to ${r.host}`, hint: `As ${r.username}, through the server's Docker` }]}
                    run={(onStep) => api.testRegistry(r.id, onStep)}
                  />
                  <Button variant="ghost" size="icon-sm" title="Edit" aria-label="Edit" onClick={() => setEditing({ step: "edit", registry: r })}>
                    <Pencil />
                  </Button>
                  <ConfirmDialog
                    trigger={
                      <Button variant="ghost" size="icon-sm" title="Remove" aria-label="Remove" className="text-muted-foreground hover:text-destructive">
                        <Trash2 />
                      </Button>
                    }
                    title={`Remove ${r.host}?`}
                    description="Images already pulled keep running; the next pull from this registry is anonymous."
                    confirmLabel="Remove"
                    onConfirm={() => remove.mutate(r.id)}
                  />
                </div>
              </Card>
            );
          })}
        </div>
      )}
      <Dialog open={editing !== null} onOpenChange={(o) => !o && setEditing(null)}>
        <DialogContent className="sm:max-w-lg">
          {editing?.step === "pick" && (
            <>
              <DialogHeader>
                <DialogTitle>Add a registry</DialogTitle>
                <DialogDescription>Where are your private images?</DialogDescription>
              </DialogHeader>
              <div className="grid gap-3 sm:grid-cols-2">
                {providers.map((p) => (
                  <ChoiceTile
                    key={p.id}
                    icon={p.icon}
                    title={p.label}
                    description={p.description}
                    disabled={!!p.host && registries.data?.some((r) => r.host === p.host)}
                    onClick={() => setEditing({ step: "new", provider: p })}
                  />
                ))}
              </div>
            </>
          )}
          {editing?.step === "new" && (
            <RegistryForm
              key={editing.provider.id}
              provider={editing.provider}
              registry={null}
              onBack={() => setEditing({ step: "pick" })}
              onDone={() => setEditing(null)}
            />
          )}
          {editing?.step === "edit" && (
            <RegistryForm
              key={editing.registry.id}
              provider={providerFor(editing.registry.host)}
              registry={editing.registry}
              onDone={() => setEditing(null)}
            />
          )}
        </DialogContent>
      </Dialog>
    </SettingsPage>
  );
}

function RegistryForm({
  provider: p,
  registry,
  onBack,
  onDone,
}: {
  provider: Provider;
  registry: Registry | null;
  onBack?: () => void;
  onDone: () => void;
}) {
  const qc = useQueryClient();
  const [form, setForm] = useState<RegistryInput>(
    registry ? { host: registry.host, username: registry.username, password: registry.password } : { host: p.host, username: "", password: "" },
  );
  const set = (k: keyof RegistryInput) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });
  const save = useMutation({
    meta: { error: "Couldn't save the registry" },
    mutationFn: () => (registry ? api.updateRegistry(registry.id, form) : api.createRegistry(form)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["registries"] });
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
        <DialogTitle className="flex items-center gap-2 [&>svg]:size-5">
          {p.icon}
          {registry ? `Edit ${registry.host}` : `Connect ${p.label}`}
        </DialogTitle>
        <DialogDescription>{p.help} The registry checks them before they're saved.</DialogDescription>
      </DialogHeader>
      {!registry && p.guide && <HowTo label="How do I get a token?" steps={p.guide} />}
      <div className="space-y-4">
        {p.id === "other" && (
          <FloatingInput
            label="Registry host"
            required
            autoFocus
            value={form.host}
            onChange={set("host")}
            placeholder="registry.example.com"
            description="As in image names, e.g. registry.example.com:5000 or quay.io."
          />
        )}
        <FloatingInput label={p.username} required autoFocus={p.id !== "other"} value={form.username} onChange={set("username")} autoComplete="off" />
        <FloatingInput
          label={p.password}
          required
          type="password"
          value={form.password}
          onChange={set("password")}
          autoComplete="new-password"
          description={registry ? "Leave as is to keep the saved one." : undefined}
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
          {registry ? "Save" : "Connect"}
        </Button>
      </DialogFooter>
    </form>
  );
}
