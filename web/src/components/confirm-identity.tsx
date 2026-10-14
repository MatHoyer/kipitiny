import { useMutation, useQuery } from "@tanstack/react-query";
import { LogIn } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { FloatingInput } from "@/components/ui/floating-input";
import { friendlyError } from "@/lib/errors";
import { signInWithSSO } from "@/lib/sso";
import { api } from "@/api";

/**
 * Confirms a sensitive change: the account's password, or for an account without one (single sign-on only) a sign-in
 * less than 10 minutes old. `ready` tells when the change may be sent.
 */
export function useConfirmation() {
  const account = useQuery({ queryKey: ["account"], queryFn: api.account });
  const [password, setPassword] = useState("");
  const hasPassword = account.data?.hasPassword ?? true;
  const ready = hasPassword ? password !== "" : !!account.data?.recentSignIn;
  return { password, setPassword, ready, hasPassword, account: account.data };
}

export function ConfirmField({ confirm, autoFocus }: { confirm: ReturnType<typeof useConfirmation>; autoFocus?: boolean }) {
  const signIn = useMutation({
    meta: { error: false },
    mutationFn: signInWithSSO,
    onError: (err) => toast.error("Couldn't sign in", { description: friendlyError(err) }),
  });
  const { account } = confirm;
  if (confirm.hasPassword)
    return (
      <FloatingInput
        label="Your password"
        type="password"
        required
        autoFocus={autoFocus}
        autoComplete="current-password"
        value={confirm.password}
        onChange={(e) => confirm.setPassword(e.target.value)}
      />
    );
  if (!account) return null;
  if (account.recentSignIn) return <p className="text-sm text-muted-foreground">Your account has no password: your recent sign-in confirms it.</p>;
  return (
    <div className="space-y-3 rounded-lg border p-3">
      <p className="text-sm text-muted-foreground">Your account has no password: sign in again to confirm, then do this within 10 minutes.</p>
      <div className="flex flex-wrap gap-2">
        {account.sso.map((i) => (
          <Button
            key={i.id}
            type="button"
            size="sm"
            variant="outline"
            loading={signIn.isPending && signIn.variables === i.providerId}
            disabled={signIn.isPending}
            onClick={() => signIn.mutate(i.providerId)}
          >
            <LogIn data-icon="inline-start" />
            Sign in with {i.providerName}
          </Button>
        ))}
      </div>
    </div>
  );
}
