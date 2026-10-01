import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, KeyRound, Plus } from "lucide-react";
import { useState, type FormEvent, type ReactNode } from "react";
import { ProtonIcon } from "@/components/brand-icons";
import { ChoiceTile, EmptyState, ErrorText, Mono, Tag, withCode } from "@/components/common";
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
  DialogTrigger,
} from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { api, type SecretProvider } from "@/api";
import { SettingsPage } from "./page";

/** Brand marks by provider id; others get a key. */
const icons: Record<string, ReactNode> = {
  protonpass: <ProtonIcon className="text-[#6D4AFF]" />,
};
const iconFor = (id: string) => icons[id] ?? <KeyRound />;

/** Password managers that service env can reference (e.g. pass://Vault/Item/field). */
export function PasswordManagers() {
  const providers = useQuery({ queryKey: ["secret-providers"], queryFn: api.secretProviders });
  const connected = providers.data?.filter((p) => p.connected) ?? [];

  return (
    <SettingsPage actions={connected.length > 0 && <ConnectDialog providers={providers.data ?? []} />}>
      <ErrorText error={providers.error} />
      {providers.data && connected.length === 0 ? (
        <EmptyState
          icon={KeyRound}
          title="No password manager connected"
          description="Reference secrets in a service's env instead of pasting them. kipitiny fetches them on each deploy and never stores them."
          action={<ConnectDialog providers={providers.data} />}
        />
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {connected.map((p) => (
            <ProviderCard key={p.id} provider={p} />
          ))}
        </div>
      )}
    </SettingsPage>
  );
}

function ProviderCard({ provider: p }: { provider: SecretProvider }) {
  const qc = useQueryClient();
  const disconnect = useMutation({
    meta: { error: `Couldn't disconnect ${p.name}` },
    mutationFn: () => api.disconnectSecretProvider(p.id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["secret-providers"] }),
  });

  return (
    <Card className="gap-3 px-4">
      <div className="flex items-start gap-3">
        <span className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-muted [&>svg]:size-5">{iconFor(p.id)}</span>
        <div className="min-w-0 flex-1">
          <p className="font-medium">{p.name}</p>
          <p className="text-xs text-muted-foreground">Connected</p>
        </div>
        <Tag className="font-mono">{p.scheme}://</Tag>
      </div>
      <p className="text-xs text-muted-foreground">
        Use in env as <Mono>{`{{ ${p.example} }}`}</Mono>
      </p>
      <div className="mt-auto flex justify-end">
        <ConfirmDialog
          trigger={
            <Button variant="outline" size="sm" disabled={disconnect.isPending}>
              Disconnect
            </Button>
          }
          title={`Disconnect ${p.name}?`}
          description={`Services referencing ${p.scheme}:// can't deploy until it's connected again. Running containers keep their values.`}
          confirmLabel="Disconnect"
          onConfirm={() => disconnect.mutate()}
        />
      </div>
    </Card>
  );
}

/** Pick a provider, then give it a token. */
function ConnectDialog({ providers }: { providers: SecretProvider[] }) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [picked, setPicked] = useState<SecretProvider | null>(null);
  const [token, setToken] = useState("");
  const onOpenChange = (next: boolean) => {
    setOpen(next);
    if (!next) {
      setPicked(null);
      setToken("");
      connect.reset();
    }
  };
  const connect = useMutation({
    meta: { error: "Couldn't connect the password manager" },
    mutationFn: () => api.connectSecretProvider(picked!.id, token.trim()),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["secret-providers"] });
      onOpenChange(false);
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    connect.mutate();
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button size="sm">
          <Plus data-icon="inline-start" />
          Connect password manager
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-xl">
        {!picked ? (
          <>
            <DialogHeader>
              <DialogTitle>Connect a password manager</DialogTitle>
              <DialogDescription>Choose where your secrets live.</DialogDescription>
            </DialogHeader>
            <div className="grid gap-3 sm:grid-cols-2">
              {providers.map((p) => (
                <ChoiceTile
                  key={p.id}
                  icon={iconFor(p.id)}
                  title={p.name}
                  description={p.available ? <Mono>{p.scheme}://</Mono> : "Its CLI isn't installed on the manager."}
                  badge={p.connected && <Tag>connected</Tag>}
                  disabled={p.connected || !p.available}
                  onClick={() => setPicked(p)}
                />
              ))}
            </div>
          </>
        ) : (
          <form onSubmit={onSubmit} className="contents">
            <DialogHeader>
              <DialogTitle className="flex items-center gap-2 [&>svg]:size-5">
                {iconFor(picked.id)}
                Connect {picked.name}
              </DialogTitle>
              {picked.help && <DialogDescription>{withCode(picked.help)}</DialogDescription>}
            </DialogHeader>
            <FloatingInput
              label={picked.tokenLabel}
              type="password"
              required
              autoFocus
              autoComplete="off"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              description={
                <>
                  Then reference secrets as <Mono>{`{{ ${picked.example} }}`}</Mono>.
                </>
              }
            />
            <DialogFooter>
              <Button type="button" variant="ghost" disabled={connect.isPending} onClick={() => setPicked(null)}>
                <ChevronLeft data-icon="inline-start" />
                Back
              </Button>
              <Button type="submit" loading={connect.isPending}>
                Connect
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
