import type { ButtonHTMLAttributes, InputHTMLAttributes, ReactNode, TextareaHTMLAttributes } from "react";

type Variant = "primary" | "secondary" | "danger";

const variants: Record<Variant, string> = {
  primary: "bg-zinc-900 text-white hover:bg-zinc-700 dark:bg-zinc-100 dark:text-zinc-900 dark:hover:bg-zinc-300",
  secondary:
    "border border-zinc-300 bg-white hover:bg-zinc-100 dark:border-zinc-700 dark:bg-zinc-900 dark:hover:bg-zinc-800",
  danger: "border border-red-300 text-red-700 hover:bg-red-50 dark:border-red-900 dark:text-red-400 dark:hover:bg-red-950",
};

export function Button({
  variant = "primary",
  className = "",
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant }) {
  return (
    <button
      className={`rounded-md px-3 py-1.5 text-sm font-medium transition-colors disabled:opacity-50 ${variants[variant]} ${className}`}
      {...props}
    />
  );
}

const inputClass =
  "w-full rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm placeholder:text-zinc-400 dark:border-zinc-700 dark:bg-zinc-900";

export function Input({ className = "", ...props }: InputHTMLAttributes<HTMLInputElement>) {
  return <input className={`${inputClass} ${className}`} {...props} />;
}

export function Textarea({ className = "", ...props }: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea className={`${inputClass} font-mono ${className}`} {...props} />;
}

export function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <label className="block space-y-1">
      <span className="text-xs font-medium text-zinc-600 dark:text-zinc-400">{label}</span>
      {children}
      {hint && <span className="block text-xs text-zinc-500">{hint}</span>}
    </label>
  );
}

export function Card({ title, actions, children }: { title?: string; actions?: ReactNode; children: ReactNode }) {
  return (
    <section className="rounded-lg border border-zinc-200 bg-white dark:border-zinc-800 dark:bg-zinc-900">
      {(title || actions) && (
        <header className="flex items-center justify-between border-b border-zinc-200 px-4 py-2.5 dark:border-zinc-800">
          <h2 className="text-sm font-semibold">{title}</h2>
          <div className="flex gap-2">{actions}</div>
        </header>
      )}
      <div className="p-4">{children}</div>
    </section>
  );
}

const stateColors: Record<string, string> = {
  running: "bg-emerald-500",
  succeeded: "bg-emerald-500",
  deploying: "animate-pulse bg-sky-500",
  degraded: "bg-amber-500",
  restarting: "bg-amber-500",
  created: "bg-amber-500",
  dead: "bg-red-500",
  failed: "bg-red-500",
};

export function StateBadge({ state }: { state: string }) {
  return (
    <span className="inline-flex items-center gap-1.5 text-xs text-zinc-600 dark:text-zinc-400">
      <span className={`size-2 rounded-full ${stateColors[state] ?? "bg-zinc-400"}`} />
      {state}
    </span>
  );
}

export function ErrorText({ error }: { error: Error | null }) {
  return error ? <p className="text-sm text-red-600 dark:text-red-400">{error.message}</p> : null;
}

/** Parses KEY=VALUE lines; blank lines and # comments are ignored. */
export function parseEnv(text: string): Record<string, string> {
  const env: Record<string, string> = {};
  for (const raw of text.split("\n")) {
    const line = raw.trim();
    if (!line || line.startsWith("#")) continue;
    const i = line.indexOf("=");
    if (i === -1) env[line] = "";
    else env[line.slice(0, i).trim()] = line.slice(i + 1);
  }
  return env;
}

export function formatEnv(env: Record<string, string>): string {
  return Object.entries(env)
    .map(([k, v]) => `${k}=${v}`)
    .join("\n");
}

export function timeAgo(iso: string): string {
  const s = Math.round((Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  if (s < 86400) return `${Math.round(s / 3600)}h ago`;
  return new Date(iso).toLocaleDateString();
}

/** Aggregate state of a service from its containers. */
export function serviceState(containers: { state: string }[]): string {
  if (containers.length === 0) return "not deployed";
  if (containers.every((c) => c.state === "running")) return "running";
  if (containers.some((c) => c.state === "running")) return "degraded";
  return containers[0].state;
}
