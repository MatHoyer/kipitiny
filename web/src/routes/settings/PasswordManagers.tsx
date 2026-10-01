import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Mono, Section, Tag } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { FloatingInput } from "@/components/ui/floating-input";
import { api, type SecretProvider } from "@/api";

/** Password managers that service env can reference (e.g. pass://Vault/Item/field). */
export function PasswordManagers() {
  const providers = useQuery({ queryKey: ["secret-providers"], queryFn: api.secretProviders });
  return (
    <Section
      title="Password managers"
      description={
        <>
          Reference a secret in a service's env instead of pasting it, e.g.{" "}
          <Mono>{"{{ pass://Vault/Item/password }}"}</Mono>. kipitiny fetches it on each deploy and never stores it.
        </>
      }
    >
      <ul className="-my-2 divide-y">
        {providers.data?.map((p) => (
          <SecretProviderRow key={p.id} provider={p} />
        ))}
      </ul>
    </Section>
  );
}

function SecretProviderRow({ provider: p }: { provider: SecretProvider }) {
  const qc = useQueryClient();
  const [token, setToken] = useState("");
  const refresh = () => qc.invalidateQueries({ queryKey: ["secret-providers"] });
  const connect = useMutation({
    meta: { error: `Couldn't connect ${p.name}` },
    mutationFn: () => api.connectSecretProvider(p.id, token.trim()),
    onSuccess: () => {
      setToken("");
      refresh();
    },
  });
  const disconnect = useMutation({
    meta: { error: `Couldn't disconnect ${p.name}` },
    mutationFn: () => api.disconnectSecretProvider(p.id),
    onSuccess: refresh,
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    connect.mutate();
  };

  return (
    <li className="space-y-2 py-2.5">
      <div className="flex items-center justify-between gap-4">
        <p className="flex items-center gap-2 text-sm font-medium">
          {p.name}
          <Tag>{p.scheme}://</Tag>
          {p.connected && <Tag>connected</Tag>}
        </p>
        {p.connected && (
          <ConfirmDialog
            trigger={
              <Button variant="outline" size="sm">
                Disconnect
              </Button>
            }
            title={`Disconnect ${p.name}?`}
            description={`Services referencing ${p.scheme}:// can't deploy until it's connected again. Running containers keep their values.`}
            confirmLabel="Disconnect"
            onConfirm={() => disconnect.mutate()}
          />
        )}
      </div>
      {!p.connected && p.help && <p className="text-xs text-muted-foreground">{withCode(p.help)}</p>}
      {!p.available ? (
        <p className="text-sm text-muted-foreground">Not available: its CLI isn't installed on the manager.</p>
      ) : (
        !p.connected && (
          <form onSubmit={onSubmit} className="flex items-start gap-2">
            <FloatingInput
              label={p.tokenLabel}
              type="password"
              required
              autoComplete="off"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              className="flex-1"
            />
            <Button type="submit" className="h-14" disabled={connect.isPending}>
              Connect
            </Button>
          </form>
        )
      )}
    </li>
  );
}

/** Renders `code` spans of plain text as Mono. */
function withCode(text: string) {
  return text.split("`").map((part, i) => (i % 2 ? <Mono key={i}>{part}</Mono> : part));
}
