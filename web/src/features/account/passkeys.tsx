import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Fingerprint, KeyRound, Plus, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { EmptyState } from "@kipit-labs/kipitidy/empty-state";
import { Section } from "@kipit-labs/kipitidy/section";
import { ConfirmDialog } from "@kipit-labs/kipitidy/confirm-dialog";
import { Button } from "@kipit-labs/kipitidy/button";
import { Dialog, DialogContent } from "@kipit-labs/kipitidy/dialog";
import { FloatingInput } from "@kipit-labs/kipitidy/floating-input";
import { friendlyError } from "@/lib/errors";
import { timeAgo } from "@/lib/format";
import { isCancelled, passkeysSupported } from "@/lib/webauthn";
import { api, type Account } from "@/api";
import { ConfirmPassword } from "./confirm-password";

export function Passkeys({ account }: { account: Account }) {
  const qc = useQueryClient();
  const [adding, setAdding] = useState(false);
  const supported = passkeysSupported();
  const remove = useMutation({
    meta: { error: "Couldn't remove the passkey" },
    mutationFn: api.deletePasskey,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["account"] }),
  });
  const add = (
    <Button size="sm" onClick={() => setAdding(true)} disabled={!supported}>
      <Plus data-icon="inline-start" />
      Add passkey
    </Button>
  );
  return (
    <Section
      title="Passkeys"
      description="Sign in with your fingerprint, face or device PIN instead of a password. A passkey stands in for two-factor authentication too."
      actions={account.passkeys.length > 0 && add}
    >
      {account.ssoRequired && (
        <p className="text-sm text-amber-600 dark:text-amber-400">
          This server requires single sign-on: passkeys don't sign in for now. They work again if an admin stops requiring it.
        </p>
      )}
      {!supported && (
        <p className="text-sm text-amber-600 dark:text-amber-400">
          Passkeys need the manager to be opened over HTTPS at a domain name (or at localhost), in a browser that supports them.
        </p>
      )}
      {account.passkeys.length === 0 ? (
        <EmptyState
          icon={Fingerprint}
          title="No passkeys yet"
          description={`A passkey only works at ${window.location.hostname}, where it was added.`}
          action={add}
        />
      ) : (
        <ul className="divide-y rounded-lg border">
          {account.passkeys.map((p) => (
            <li key={p.id} className="flex items-center gap-3 px-4 py-3">
              <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted [&>svg]:size-4">
                <KeyRound />
              </span>
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium">{p.name}</p>
                <p className="truncate text-xs text-muted-foreground">
                  Added {timeAgo(p.createdAt)} · {p.lastUsedAt ? `last used ${timeAgo(p.lastUsedAt)}` : "never used"}
                </p>
              </div>
              <ConfirmDialog
                trigger={
                  <Button variant="ghost" size="icon-sm" title="Remove" aria-label="Remove" className="text-muted-foreground hover:text-destructive">
                    <Trash2 />
                  </Button>
                }
                title={`Remove passkey ${p.name}?`}
                description="It won't sign you in anymore. Also delete it from the device or password manager holding it."
                confirmLabel="Remove"
                onConfirm={() => remove.mutate(p.id)}
              />
            </li>
          ))}
        </ul>
      )}
      <Dialog open={adding} onOpenChange={setAdding}>
        <DialogContent className="sm:max-w-md">{adding && <AddPasskey onDone={() => setAdding(false)} />}</DialogContent>
      </Dialog>
    </Section>
  );
}

function AddPasskey({ onDone }: { onDone: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const add = useMutation({
    meta: { error: false },
    mutationFn: (password: string) => api.addPasskey(password, name.trim()),
    onSuccess: (p) => {
      qc.invalidateQueries({ queryKey: ["account"] });
      toast.success(`Passkey ${p.name} added`);
      onDone();
    },
    onError: (err) => !isCancelled(err) && toast.error("Couldn't add the passkey", { description: friendlyError(err), duration: 8000 }),
  });
  return (
    <ConfirmPassword
      title="Add a passkey"
      description="Confirm your password, then follow your browser's prompt."
      submitLabel="Continue"
      pending={add.isPending}
      onSubmit={(p) => add.mutate(p)}
    >
      <FloatingInput
        label="Name"
        required
        autoFocus
        maxLength={60}
        placeholder="MacBook Touch ID"
        value={name}
        onChange={(e) => setName(e.target.value)}
      />
    </ConfirmPassword>
  );
}
