import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Link } from "react-router";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingSelect } from "@/components/ui/floating-select";
import { cn } from "@/lib/utils";
import { api } from "../api";

const NONE = "__none";
const OTHER = "__other";

/** Splits a full domain into subdomain and the longest listed base domain. */
function split(value: string, bases: string[]): { base: string; sub: string } {
  if (!value) return { base: NONE, sub: "" };
  const base = bases
    .filter((b) => value === b || value.endsWith(`.${b}`))
    .sort((a, b) => b.length - a.length)[0];
  if (!base) return { base: OTHER, sub: value };
  return { base, sub: value === base ? "" : value.slice(0, -(base.length + 1)) };
}

/**
 * A service's public domain: a subdomain plus one of the domains listed in
 * Settings, like Cloudflare's hostname form. "Other domain" takes any full
 * name; with no listed domains it's a plain input.
 */
export function DomainField({
  value,
  onChange,
  className,
}: {
  value: string;
  onChange: (domain: string) => void;
  className?: string;
}) {
  const domains = useQuery({ queryKey: ["domains"], queryFn: api.domains });
  const bases = domains.data?.map((d) => d.name) ?? [];
  const [base, setBase] = useState(NONE);
  const [sub, setSub] = useState("");

  // Split the initial value once the list is known; after that the fields
  // are the source of truth (so "Other" doesn't snap back to a listed base).
  const [ready, setReady] = useState(false);
  useEffect(() => {
    if (!domains.data || ready) return;
    const s = split(value, bases);
    setBase(s.base);
    setSub(s.sub);
    setReady(true);
  }, [domains.data]); // only when the list first arrives

  const update = (nextBase: string, nextSub: string) => {
    setBase(nextBase);
    setSub(nextSub);
    const s = nextSub.trim().toLowerCase();
    if (nextBase === NONE) onChange("");
    else if (nextBase === OTHER) onChange(s);
    else onChange(s ? `${s}.${nextBase}` : nextBase);
  };

  // What the text field should hold after switching to base b.
  const subFor = (b: string) => {
    if (b === OTHER) return value;
    if (base !== OTHER) return sub;
    const s = split(value, [b]); // keep "app" when app.example.com matches b
    return s.base === b ? s.sub : "";
  };

  const noList = domains.isSuccess && bases.length === 0;
  if (noList || !ready) {
    return (
      <FloatingInput
        label="Domain"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="app.example.com"
        className={className}
        description={
          <>
            Empty for a private service, reachable only inside the project.{" "}
            {noList && (
              <>
                List your domains in{" "}
                <Link to="/settings" className="underline underline-offset-2">
                  Settings
                </Link>{" "}
                to pick them here.
              </>
            )}
          </>
        }
      />
    );
  }

  return (
    <div className={cn("space-y-1.5", className)}>
      <div className="grid gap-3 sm:grid-cols-2">
        {base !== NONE && (
          <FloatingInput
            label={base === OTHER ? "Full domain" : "Subdomain"}
            value={sub}
            onChange={(e) => update(base, e.target.value)}
            placeholder={base === OTHER ? "app.example.com" : "app (empty for the domain itself)"}
          />
        )}
        <FloatingSelect
          label="Domain"
          value={base}
          onValueChange={(b) => update(b, subFor(b))}
          className={base === NONE ? "sm:col-span-2" : undefined}
          options={[
            { value: NONE, label: "Private (no domain)" },
            ...bases.map((b) => ({ value: b, label: b })),
            { value: OTHER, label: "Other domain…" },
          ]}
        />
      </div>
      <p className="px-1 text-xs text-muted-foreground">
        {value ? (
          <>
            Served at <span className="font-mono">https://{value}</span>
          </>
        ) : (
          "Private: reachable only inside the project."
        )}
      </p>
    </div>
  );
}
