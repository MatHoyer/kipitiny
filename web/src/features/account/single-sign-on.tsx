import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link2, LogIn, Trash2 } from "lucide-react";
import { useState } from "react";
import { EmptyState } from "@kipit-labs/kipitidy/empty-state";
import { Section } from "@kipit-labs/kipitidy/section";
import { ConfirmDialog } from "@kipit-labs/kipitidy/confirm-dialog";
import { Button } from "@kipit-labs/kipitidy/button";
import { Dialog, DialogContent } from "@kipit-labs/kipitidy/dialog";
import { timeAgo } from "@/lib/format";
import { leaveFor } from "@/lib/sso";
import { api, type Account, type SSOButton } from "@/api";
import { ConfirmPassword } from "./confirm-password";

/** Identities at single sign-on providers that sign in to this account. */
export function SingleSignOn({ account }: { account: Account }) {
  const qc = useQueryClient();
  const [linking, setLinking] = useState<SSOButton | null>(null);
  const unlink = useMutation({
    meta: { error: "Couldn't unlink the account" },
    mutationFn: api.unlinkSSO,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["account"] }),
  });
  const unlinked = account.ssoProviders.filter((p) => !account.sso.some((i) => i.providerId === p.id));
  if (account.ssoProviders.length === 0 && account.sso.length === 0) return null;
  const links = unlinked.length > 0 && (
    <div className="flex flex-wrap gap-2">
      {unlinked.map((p) => (
        <Button key={p.id} size="sm" variant={account.sso.length ? "outline" : "default"} onClick={() => setLinking(p)}>
          <Link2 data-icon="inline-start" />
          Link {p.name}
        </Button>
      ))}
    </div>
  );
  return (
    <Section
      title="Single sign-on"
      description="Sign in with your account at an identity provider. Two-factor authentication still applies when it's on."
      actions={account.sso.length > 0 && links}
    >
      {account.sso.length === 0 ? (
        <EmptyState icon={LogIn} title="No linked accounts" description="Link one to see a Sign in with … button on the sign-in page." action={links} />
      ) : (
        <ul className="divide-y rounded-lg border">
          {account.sso.map((i) => (
            <li key={i.id} className="flex items-center gap-3 px-4 py-3">
              <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted [&>svg]:size-4">
                <LogIn />
              </span>
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium">
                  {i.providerName}
                  {i.email && <span className="font-normal text-muted-foreground"> · {i.email}</span>}
                </p>
                <p className="truncate text-xs text-muted-foreground">
                  Linked {timeAgo(i.createdAt)} · {i.lastUsedAt ? `last used ${timeAgo(i.lastUsedAt)}` : "never used"}
                </p>
              </div>
              <ConfirmDialog
                trigger={
                  <Button variant="ghost" size="icon-sm" title="Unlink" aria-label="Unlink" className="text-muted-foreground hover:text-destructive">
                    <Trash2 />
                  </Button>
                }
                title={`Unlink ${i.providerName}?`}
                description="It won't sign you in anymore. You can link it again later."
                confirmLabel="Unlink"
                onConfirm={() => unlink.mutate(i.id)}
              />
            </li>
          ))}
        </ul>
      )}
      <Dialog open={linking !== null} onOpenChange={(o) => !o && setLinking(null)}>
        <DialogContent className="sm:max-w-md">{linking && <LinkSSO provider={linking} />}</DialogContent>
      </Dialog>
    </Section>
  );
}

function LinkSSO({ provider }: { provider: SSOButton }) {
  const link = useMutation({
    meta: { error: "Couldn't link the account" },
    mutationFn: async (password: string) => leaveFor((await api.connectSSO(provider.id, password)).url),
  });
  return (
    <ConfirmPassword
      title={`Link ${provider.name}`}
      description={`Confirm your password, then sign in at ${provider.name}: the account you use there will sign in here.`}
      submitLabel="Continue"
      pending={link.isPending}
      onSubmit={(p) => link.mutate(p)}
    />
  );
}
