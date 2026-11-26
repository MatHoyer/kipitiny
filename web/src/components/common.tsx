import { CircleCheck, CircleSlash, CircleX } from "lucide-react";
import type { ReactNode } from "react";
import { CopyButton } from "@kipit-labs/kipitidy/copy-button";
import { Spinner } from "@kipit-labs/kipitidy/spinner";
import { friendlyError } from "@/lib/errors";
import { cn } from "@/lib/utils";

export const stateColors: Record<string, string> = {
  running: "bg-emerald-500",
  healthy: "bg-emerald-500",
  starting: "animate-pulse bg-amber-500",
  unhealthy: "bg-red-500",
  succeeded: "bg-emerald-500",
  completed: "bg-emerald-500",
  skipped: "bg-pink-500",
  passed: "bg-emerald-500",
  deploying: "animate-pulse bg-sky-500",
  // Blue, not grey: asleep wakes on the next request, stopped stays down.
  asleep: "bg-blue-500",
  degraded: "bg-amber-500",
  restarting: "bg-amber-500",
  created: "bg-amber-500",
  dead: "bg-red-500",
  failed: "bg-red-500",
  up: "bg-emerald-500",
  down: "bg-red-500",
};

export function StateBadge({ state, className }: { state: string; className?: string }) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-xs whitespace-nowrap text-muted-foreground",
        className,
      )}
    >
      <span className={cn("size-1.5 rounded-full", stateColors[state] ?? "bg-muted-foreground/50")} />
      {state}
    </span>
  );
}

/** Small label, e.g. a token's access or "age". */
export function Tag({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <span className={cn("rounded-md bg-muted px-1.5 py-0.5 text-xs font-medium text-muted-foreground", className)}>
      {children}
    </span>
  );
}

export function ErrorText({ error }: { error: Error | null | undefined }) {
  return error ? (
    <p role="alert" className="text-sm wrap-break-word text-destructive">
      {friendlyError(error)}
    </p>
  ) : null;
}

export function Empty({ children }: { children: ReactNode }) {
  return <p className="text-sm text-muted-foreground">{children}</p>;
}

export function Mono({ children }: { children: ReactNode }) {
  return <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">{children}</code>;
}

/** Label/value pairs, each value copyable. */
export function SecretList({ rows }: { rows: [string, string][] }) {
  return (
    <dl className="grid grid-cols-[6rem_1fr] items-center gap-x-3 gap-y-1.5 text-sm">
      {rows.map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="text-muted-foreground">{k}</dt>
          <dd className="flex items-center gap-1 font-mono text-xs break-all">
            <span className="flex-1">{v}</span>
            <CopyButton value={v} />
          </dd>
        </div>
      ))}
    </dl>
  );
}

/** Renders `code` spans of plain text (e.g. help from the API) as Mono. */
export function withCode(text: string) {
  return text.split("`").map((part, i) => (i % 2 ? <Mono key={i}>{part}</Mono> : part));
}

/** The icon of an operation's status: a deployment's or a job run's. */
export const statusIcon = {
  succeeded: <CircleCheck className="size-4.5 text-emerald-600 dark:text-emerald-400" />,
  failed: <CircleX className="size-4.5 text-destructive" />,
  skipped: <CircleSlash className="size-4.5 text-pink-500" />,
  running: <Spinner className="size-4.5 text-sky-500" />,
} satisfies Record<string, ReactNode>;

/** Who started an operation, from user:<name> or token:<name>. */
export function triggeredBy(t?: string) {
  if (!t) return "";
  const [kind, name] = [t.slice(0, t.indexOf(":")), t.slice(t.indexOf(":") + 1)];
  if (kind === "token") return `token ${name}`;
  return name;
}
