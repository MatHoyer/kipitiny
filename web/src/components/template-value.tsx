import { ChevronRight, Link2 } from "lucide-react";
import { PostgresIcon, schemeIcon } from "@/components/brand-icons";
import { splitRefs, type EnvReference } from "@/lib/format";
import { cn } from "@/lib/utils";

/**
 * An env value with its references as chips: text{{ project.NAME }} shows
 * the text, then a chip for NAME. maskText hides the literal text (an unsaved
 * secret); invalid marks references that don't resolve.
 */
export function TemplateValue({
  value,
  maskText = false,
  invalid,
  className,
}: {
  value: string;
  maskText?: boolean;
  invalid?: (r: EnvReference) => boolean;
  className?: string;
}) {
  const parts = splitRefs(value);
  if (parts.length === 0) return <span className={cn("text-muted-foreground italic", className)}>empty</span>;
  return (
    <span className={cn("flex min-w-0 items-center gap-0.5 overflow-hidden font-mono text-xs", className)} title={maskText ? undefined : value}>
      {parts.map((p, i) =>
        "text" in p ? (
          <span key={i} className={cn("truncate whitespace-pre", maskText && "tracking-widest text-muted-foreground")}>
            {maskText ? "•".repeat(Math.min(p.text.length, 8)) : p.text}
          </span>
        ) : (
          <RefChip key={i} r={p.ref} invalid={invalid?.(p.ref)} />
        ),
      )}
    </span>
  );
}

function RefChip({ r, invalid }: { r: EnvReference; invalid?: boolean }) {
  return (
    <span
      className={cn(
        "inline-flex min-w-0 shrink items-center gap-1 rounded-md bg-muted px-1.5 py-0.5",
        invalid && "bg-destructive/10 text-destructive",
      )}
    >
      {r.kind === "db" ? (
        <PostgresIcon className="size-3 shrink-0 text-[#4169E1]" />
      ) : r.kind === "secret" ? (
        <span className="flex shrink-0 [&>svg]:size-3">{schemeIcon(r.ref.split("://")[0])}</span>
      ) : (
        <Link2 className="size-3 shrink-0" />
      )}
      <span className="truncate">{r.kind === "db" ? `${r.db}.${r.field}` : r.kind === "secret" ? r.ref : r.name}</span>
    </span>
  );
}

/**
 * Where a password manager reference points: the manager's mark, then its
 * vault › item › field. invalid marks one no connected manager resolves.
 */
export function ManagerRefValue({
  scheme,
  path,
  invalid,
  className,
}: {
  scheme: string;
  path: string[];
  invalid?: boolean;
  className?: string;
}) {
  const field = path.length > 2 ? path[path.length - 1] : null;
  const where = field ? path.slice(0, -1) : path;
  return (
    <span
      className={cn("flex min-w-0 items-center gap-1.5 text-xs", invalid && "text-destructive", className)}
      title={`${scheme}://${path.join("/")}`}
    >
      <span className="flex shrink-0 [&>svg]:size-3.5">{schemeIcon(scheme)}</span>
      {where.map((p, i) => (
        <span key={i} className="flex min-w-0 items-center gap-1.5">
          {i > 0 && <ChevronRight className="size-3 shrink-0 text-muted-foreground" />}
          <span className={cn("truncate", i === where.length - 1 && "font-medium")}>{p}</span>
        </span>
      ))}
      {field && (
        <span className="shrink-0 rounded-md bg-muted px-1.5 py-0.5 font-mono text-[0.7rem] text-muted-foreground">{field}</span>
      )}
    </span>
  );
}
