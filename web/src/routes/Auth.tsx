import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Box, Loader2 } from "lucide-react";
import { useState, type FormEvent, type ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { FloatingInput } from "@/components/ui/floating-input";
import { toast } from "sonner";
import { friendlyError } from "@/lib/errors";
import { api } from "../api";

function Shell({ title, subtitle, children }: { title: string; subtitle?: string; children: ReactNode }) {
  return (
    <div className="flex min-h-svh items-center justify-center bg-muted/40 p-4">
      <div className="w-full max-w-sm space-y-6">
        <p className="flex items-center justify-center gap-2 text-lg font-semibold tracking-tight">
          <Box className="size-6" />
          kipitiny
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
  const login = useMutation({
    meta: { error: false },
    mutationFn: () => api.login(username.trim(), password),
    onSuccess: () => qc.resetQueries(),
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
