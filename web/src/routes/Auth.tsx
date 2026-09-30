import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { api } from "../api";
import { Button, ErrorText, Field, Input } from "../ui";

function Shell({ title, subtitle, children }: { title: string; subtitle?: string; children: React.ReactNode }) {
  return (
    <div className="flex min-h-screen items-center justify-center bg-zinc-50 px-4 text-zinc-900 dark:bg-zinc-950 dark:text-zinc-100">
      <div className="w-full max-w-sm space-y-6 rounded-lg border border-zinc-200 bg-white p-6 dark:border-zinc-800 dark:bg-zinc-900">
        <div className="space-y-1">
          <p className="text-sm font-semibold tracking-tight text-zinc-500">kipitiny</p>
          <h1 className="text-lg font-semibold">{title}</h1>
          {subtitle && <p className="text-sm text-zinc-500">{subtitle}</p>}
        </div>
        {children}
      </div>
    </div>
  );
}

export function Login() {
  const qc = useQueryClient();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const login = useMutation({
    mutationFn: () => api.login(username.trim(), password),
    onSuccess: () => qc.resetQueries(),
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    login.mutate();
  };
  return (
    <Shell title="Sign in">
      <form onSubmit={onSubmit} className="space-y-4">
        <Field label="Username">
          <Input autoFocus autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} />
        </Field>
        <Field label="Password">
          <Input
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </Field>
        <Button className="w-full" disabled={login.isPending}>
          Sign in
        </Button>
        {login.error && (
          <p className="text-sm text-red-600 dark:text-red-400">
            {login.error.message === "unauthorized" ? "Wrong username or password." : login.error.message}
          </p>
        )}
      </form>
    </Shell>
  );
}

export function Setup() {
  const qc = useQueryClient();
  const [form, setForm] = useState({ token: "", username: "admin", password: "", confirm: "" });
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });
  const setup = useMutation({
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
      <form onSubmit={onSubmit} className="space-y-4">
        <Field label="Setup token">
          <Input autoFocus required value={form.token} onChange={set("token")} className="font-mono" />
        </Field>
        <Field label="Username">
          <Input required autoComplete="username" value={form.username} onChange={set("username")} />
        </Field>
        <Field label="Password" hint="At least 10 characters.">
          <Input type="password" required autoComplete="new-password" value={form.password} onChange={set("password")} />
        </Field>
        <Field label="Confirm password">
          <Input type="password" required autoComplete="new-password" value={form.confirm} onChange={set("confirm")} />
        </Field>
        <Button className="w-full" disabled={setup.isPending}>
          Create account
        </Button>
        <ErrorText error={setup.error} />
      </form>
    </Shell>
  );
}
