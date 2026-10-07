import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Fingerprint, Loader2 } from "lucide-react";
import { useState, type FormEvent, type ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { FloatingInput } from "@/components/ui/floating-input";
import { Separator } from "@/components/ui/separator";
import { toast } from "sonner";
import { friendlyError } from "@/lib/errors";
import { isCancelled, passkeysSupported } from "@/lib/webauthn";
import { Logo } from "@/components/logo";
import { api } from "../api";

function Shell({ title, subtitle, children }: { title: string; subtitle?: string; children: ReactNode }) {
  return (
    <div className="flex min-h-svh items-center justify-center bg-muted/40 p-4">
      <div className="w-full max-w-sm space-y-6">
        <p className="flex justify-center text-2xl">
          <Logo />
        </p>
        <Card>
          <CardHeader>
            <CardTitle>{title}</CardTitle>
            {subtitle && <CardDescription>{subtitle}</CardDescription>}
          </CardHeader>
          {children}
        </Card>
      </div>
    </div>
  );
}

function Submit({ pending, children }: { pending: boolean; children: ReactNode }) {
  return (
    <CardFooter className="mt-2">
      <Button type="submit" size="lg" className="h-11 w-full rounded-xl" disabled={pending}>
        {pending && <Loader2 className="animate-spin" aria-hidden />}
        {children}
      </Button>
    </CardFooter>
  );
}

export function Login() {
  const qc = useQueryClient();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [ticket, setTicket] = useState("");
  const login = useMutation({
    meta: { error: false },
    mutationFn: () => api.login(username.trim(), password),
    onSuccess: (res) => ("mfaRequired" in res ? setTicket(res.ticket) : qc.resetQueries()),
    onError: (err) =>
      toast.error("Couldn't sign in", {
        description: err.message === "unauthorized" ? "Wrong username or password." : friendlyError(err),
      }),
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    login.mutate();
  };
  const wrong = login.error?.message === "unauthorized";
  if (ticket) return <SecondFactor ticket={ticket} onRestart={() => setTicket("")} />;
  return (
    <Shell title="Sign in">
      <form onSubmit={onSubmit}>
        <CardContent className="space-y-4">
          <FloatingInput
            label="Username"
            autoFocus
            required
            autoComplete="username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            aria-invalid={wrong || undefined}
          />
          <FloatingInput
            label="Password"
            type="password"
            required
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            aria-invalid={wrong || undefined}
          />
        </CardContent>
        <Submit pending={login.isPending}>Sign in</Submit>
      </form>
      <PasskeySignIn />
    </Shell>
  );
}

/** Passwordless sign-in; a passkey stands in for the second factor too. */
function PasskeySignIn() {
  const qc = useQueryClient();
  const signIn = useMutation({
    meta: { error: false },
    mutationFn: api.loginPasskey,
    onSuccess: () => qc.resetQueries(),
    onError: (err) => !isCancelled(err) && toast.error("Couldn't sign in with a passkey", { description: friendlyError(err) }),
  });
  if (!passkeysSupported()) return null;
  return (
    <CardFooter className="flex-col gap-3">
      <div className="flex w-full items-center gap-3 text-xs text-muted-foreground">
        <Separator className="flex-1" />
        or
        <Separator className="flex-1" />
      </div>
      <Button
        type="button"
        variant="outline"
        size="lg"
        className="h-11 w-full rounded-xl"
        loading={signIn.isPending}
        onClick={() => signIn.mutate()}
      >
        <Fingerprint data-icon="inline-start" />
        Sign in with a passkey
      </Button>
    </CardFooter>
  );
}

function SecondFactor({ ticket, onRestart }: { ticket: string; onRestart: () => void }) {
  const qc = useQueryClient();
  const [recovery, setRecovery] = useState(false);
  const [code, setCode] = useState("");
  const verify = useMutation({
    meta: { error: false },
    mutationFn: () => api.loginMfa(ticket, code),
    onSuccess: () => qc.resetQueries(),
    onError: (err) => {
      setCode("");
      if (/expired|too many/.test(err.message)) onRestart();
      toast.error("Couldn't sign in", { description: err.message === "unauthorized" ? "Wrong code." : friendlyError(err) });
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    verify.mutate();
  };
  return (
    <Shell
      title="Two-factor authentication"
      subtitle={recovery ? "Enter one of your recovery codes. Each works once." : "Enter the 6-digit code from your authenticator app."}
    >
      <form onSubmit={onSubmit}>
        <CardContent className="space-y-4">
          {recovery ? (
            <FloatingInput
              key="recovery"
              label="Recovery code"
              autoFocus
              required
              autoComplete="off"
              placeholder="xxxxx-xxxxx"
              value={code}
              onChange={(e) => setCode(e.target.value)}
              inputClassName="font-mono"
            />
          ) : (
            <FloatingInput
              key="totp"
              label="Code"
              autoFocus
              required
              inputMode="numeric"
              autoComplete="one-time-code"
              pattern="[0-9 ]{6,7}"
              maxLength={7}
              value={code}
              onChange={(e) => setCode(e.target.value)}
              inputClassName="font-mono tracking-widest"
            />
          )}
        </CardContent>
        <Submit pending={verify.isPending}>Verify</Submit>
      </form>
      <CardFooter className="flex-wrap justify-between gap-2 text-sm">
        <Button
          variant="link"
          size="sm"
          className="h-auto px-0 text-muted-foreground"
          onClick={() => {
            setRecovery(!recovery);
            setCode("");
          }}
        >
          {recovery ? "Use the authenticator app" : "Use a recovery code"}
        </Button>
        <Button variant="link" size="sm" className="h-auto px-0 text-muted-foreground" onClick={onRestart}>
          Back
        </Button>
      </CardFooter>
      <PasskeySignIn />
    </Shell>
  );
}

export function Setup() {
  const qc = useQueryClient();
  const [form, setForm] = useState({ token: "", username: "admin", password: "", confirm: "" });
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });
  const setup = useMutation({
    meta: { error: "Couldn't create the account" },
    mutationFn: () => {
      if (form.password !== form.confirm) throw new Error("Passwords don't match.");
      return api.setup(form.token.trim(), form.username.trim(), form.password);
    },
    onSuccess: () => qc.resetQueries(),
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    setup.mutate();
  };
  return (
    <Shell title="Create the admin account" subtitle="The setup token is printed in the manager's logs on first start.">
      <form onSubmit={onSubmit}>
        <CardContent className="space-y-4">
          <FloatingInput
            label="Setup token"
            autoFocus
            required
            value={form.token}
            onChange={set("token")}
            inputClassName="font-mono"
          />
          <FloatingInput label="Username" required autoComplete="username" value={form.username} onChange={set("username")} />
          <FloatingInput
            label="Password"
            type="password"
            required
            autoComplete="new-password"
            value={form.password}
            onChange={set("password")}
            description="At least 10 characters."
          />
          <FloatingInput
            label="Confirm password"
            type="password"
            required
            autoComplete="new-password"
            value={form.confirm}
            onChange={set("confirm")}
          />
        </CardContent>
        <Submit pending={setup.isPending}>Create account</Submit>
      </form>
    </Shell>
  );
}
