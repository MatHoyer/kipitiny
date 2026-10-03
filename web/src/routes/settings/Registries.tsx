import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, Package, Pencil, Plus, Trash2 } from "lucide-react";
import { useState, type FormEvent, type ReactNode } from "react";
import { DockerIcon, GitHubIcon, GitLabIcon } from "@/components/brand-icons";
import { ChoiceTile, EmptyState, ErrorText, Mono, TestButton } from "@/components/common";
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
                  <TestButton name={r.host} test={() => api.testRegistry(r.id)} />
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
