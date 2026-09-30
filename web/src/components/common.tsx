import { CheckIcon, CopyIcon } from "lucide-react";
import { useId, useState, type ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";

/** A titled card; every block of a page is one. */
export function Section({
  title,
  description,
  actions,
  children,
  className,
}: {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  children?: ReactNode;
  className?: string;
}) {
  return (
    <Card className={className}>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        {description && <CardDescription>{description}</CardDescription>}
        {actions && <CardAction className="flex items-center gap-2">{actions}</CardAction>}
      </CardHeader>
      {children && <CardContent className="space-y-4">{children}</CardContent>}
    </Card>
  );
}

const stateColors: Record<string, string> = {
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
      {error.message}
    </p>
  ) : null;
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
