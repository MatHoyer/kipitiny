import { CheckIcon, CopyIcon, type LucideIcon } from "lucide-react";
import { useId, useState, type ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { Spinner } from "@/components/ui/spinner";
import { friendlyError } from "@/lib/errors";
import { cn } from "@/lib/utils";

/** A card, titled unless the page header already names it; every block of a page is one. */
export function Section({
  title,
  description,
  actions,
  children,
  className,
  plain,
}: {
  title?: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  children?: ReactNode;
  className?: string;
  /** No card: the block is the page's (or tab's) only content, already named by it. */
  plain?: boolean;
}) {
  if (plain)
    return (
      <section className={cn("space-y-4", className)}>
        {(title || description || actions) && (
          <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
            <div className="space-y-1">
              {title && <h2 className="font-medium">{title}</h2>}
              {description && <p className="text-sm text-muted-foreground">{description}</p>}
            </div>
            {actions && <div className="flex items-center gap-2">{actions}</div>}
          </div>
        )}
        {children}
      </section>
    );
  return (
    <Card className={className}>
      {(title || description || actions) && (
        <CardHeader>
          {title && <CardTitle>{title}</CardTitle>}
          {description && <CardDescription>{description}</CardDescription>}
          {actions && <CardAction className="flex items-center gap-2">{actions}</CardAction>}
        </CardHeader>
      )}
      {children && <CardContent className="space-y-4">{children}</CardContent>}
    </Card>
  );
}

export const stateColors: Record<string, string> = {
  running: "bg-emerald-500",
  healthy: "bg-emerald-500",
  starting: "animate-pulse bg-amber-500",
  unhealthy: "bg-red-500",
  succeeded: "bg-emerald-500",
  passed: "bg-emerald-500",
  deploying: "animate-pulse bg-sky-500",
  degraded: "bg-amber-500",
  restarting: "bg-amber-500",
  created: "bg-amber-500",
  dead: "bg-red-500",
  failed: "bg-red-500",
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

/** Small label, e.g. a token scope or "age". */
export function Tag({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <span className={cn("rounded-md bg-muted px-1.5 py-0.5 text-xs font-medium text-muted-foreground", className)}>
      {children}
    </span>
  );
}

export function ErrorText({ error }: { error: Error | null | undefined }) {
  return error ? (
    <p role="alert" className="text-sm text-destructive">
      {friendlyError(error)}
    </p>
  ) : null;
}

/** What a page or block shows while its data loads. */
export function Loading({ className }: { className?: string }) {
  return (
    <div className={cn("flex justify-center py-12 text-muted-foreground", className)}>
      <Spinner className="size-6" />
    </div>
  );
}

export function Empty({ children }: { children: ReactNode }) {
  return <p className="text-sm text-muted-foreground">{children}</p>;
}

export function Mono({ children }: { children: ReactNode }) {
  return <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">{children}</code>;
}

export function CopyButton({ value, label = "Copy" }: { value: string; label?: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <Button
      type="button"
      variant="ghost"
      size="icon-xs"
      aria-label={label}
      title={label}
      onClick={() => {
        navigator.clipboard.writeText(value);
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      }}
    >
      {copied ? <CheckIcon /> : <CopyIcon />}
    </Button>
  );
}

/** A value in a bordered box with a copy button, e.g. a token or a URL. */
export function CopyField({ value }: { value: string }) {
  return (
    <div className="flex items-start gap-1 rounded-lg border bg-muted/40 py-1 pr-1 pl-2.5">
      <code className="flex-1 py-0.5 font-mono text-xs break-all">{value}</code>
      <CopyButton value={value} />
    </div>
  );
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

export function CheckboxField({
  label,
  description,
  checked,
  onCheckedChange,
  className,
}: {
  label: ReactNode;
  description?: ReactNode;
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
  className?: string;
}) {
  const id = useId();
  return (
    <div className={cn("flex items-start gap-3", className)}>
      <Checkbox id={id} className="mt-0.5" checked={checked} onCheckedChange={(v) => onCheckedChange(v === true)} />
      <div className="grid gap-1">
        <Label htmlFor={id}>{label}</Label>
        {description && <p className="text-xs text-muted-foreground">{description}</p>}
      </div>
    </div>
  );
}

/** An icon in a tinted rounded square, the visual anchor of a card. */
export function IconTile({ icon: Icon, className }: { icon: LucideIcon; className?: string }) {
  return (
    <span className={cn("flex size-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary", className)}>
      <Icon className="size-5" />
    </span>
  );
}

/** A small metric tile, e.g. "Services 4". */
export function StatCard({
  icon: Icon,
  label,
  value,
  hint,
  tone,
}: {
  icon: LucideIcon;
  label: ReactNode;
  value: ReactNode;
  hint?: ReactNode;
  tone?: "good" | "warn" | "bad";
}) {
  return (
    <Card size="sm" className="gap-1 px-3">
      <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <Icon className="size-3.5" />
        {label}
      </p>
      <p
        className={cn(
          "text-2xl font-semibold tabular-nums",
          tone === "good" && "text-emerald-600 dark:text-emerald-400",
          tone === "warn" && "text-amber-600 dark:text-amber-400",
          tone === "bad" && "text-destructive",
        )}
      >
        {value}
      </p>
      {hint && <p className="truncate text-xs text-muted-foreground">{hint}</p>}
    </Card>
  );
}

/** What a list shows before it has anything, with the action that fills it. */
export function EmptyState({
  icon,
  title,
  description,
  action,
}: {
  icon: LucideIcon;
  title: ReactNode;
  description?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <Card className="items-center gap-3 border border-dashed py-12 text-center ring-0">
      <IconTile icon={icon} className="size-11" />
      <div className="space-y-1">
        <p className="font-medium">{title}</p>
        {description && <p className="mx-auto max-w-sm text-sm text-muted-foreground">{description}</p>}
      </div>
      {action}
    </Card>
  );
}

/** A card holding irreversible actions. */
export function DangerZone({ children, description }: { children: ReactNode; description?: ReactNode }) {
  return (
    <Card className="ring-destructive/30">
      <CardHeader>
        <CardTitle className="text-destructive">Danger zone</CardTitle>
        {description && <CardDescription>{description}</CardDescription>}
        <CardAction className="flex items-center gap-2">{children}</CardAction>
      </CardHeader>
    </Card>
  );
}

/** A large pick in a "choose one" step, e.g. a database engine or a password manager. */
export function ChoiceTile({
  icon,
  title,
  description,
  badge,
  disabled,
  onClick,
}: {
  icon: ReactNode;
  title: ReactNode;
  description?: ReactNode;
  badge?: ReactNode;
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      className="group flex flex-col items-start gap-3 rounded-xl border p-4 text-left transition-all outline-none hover:-translate-y-px hover:border-foreground/30 hover:shadow-md focus-visible:ring-3 focus-visible:ring-ring/50 disabled:pointer-events-none disabled:opacity-50"
    >
      <span className="flex w-full items-start justify-between gap-2">
        <span className="flex size-11 items-center justify-center rounded-lg bg-muted [&>svg]:size-6">{icon}</span>
        {badge}
      </span>
      <span className="space-y-0.5">
        <span className="block font-medium">{title}</span>
        {description && <span className="block text-xs text-muted-foreground">{description}</span>}
      </span>
    </button>
  );
}

/** Renders `code` spans of plain text (e.g. help from the API) as Mono. */
export function withCode(text: string) {
  return text.split("`").map((part, i) => (i % 2 ? <Mono key={i}>{part}</Mono> : part));
}
