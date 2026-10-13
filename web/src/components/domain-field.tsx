import { useQuery } from "@tanstack/react-query";
import { useEffect, useRef, useState, type ReactNode } from "react";
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

/** Cloudflare's free certificate covers a zone and *.zone: a deeper proxied name gets a TLS error at its edge. */
function edgeCertGap(value: string, zones: string[]): string | undefined {
  const zone = zones.filter((z) => value.endsWith(`.${z}`)).sort((a, b) => b.length - a.length)[0];
  if (!zone || !value.slice(0, -(zone.length + 1)).includes(".")) return;
  return zone;
}

const flat = (value: string, zone: string) => `${value.slice(0, -(zone.length + 1)).replaceAll(".", "-")}.${zone}`;

/**
 * A service's public domain: a subdomain plus one of the domains listed in
 * Settings, like Cloudflare's hostname form. "Other domain" takes any full
 * name; with no listed domains it's a plain input.
 */
export function DomainField({
  value,
  onChange,
  note,
  label = "Domain",
  emptyLabel = "Private (no domain)",
  emptyText,
  host = (domain) => domain,
  className,
}: {
  value: string;
  onChange: (domain: string) => void;
  /** Replaces "Served at …" once a domain is set, e.g. for a DNS-only one. */
  note?: ReactNode;
  label?: string;
  /** The choice for no domain, and what no domain means. */
  emptyLabel?: string;
  emptyText?: string;
  /** A hostname served under the value, checked against Cloudflare's certificate (a base domain's apps). */
  host?: (domain: string) => string;
  className?: string;
}) {
  const domains = useQuery({ queryKey: ["domains"], queryFn: api.domains });
  const bases = domains.data?.map((d) => d.name) ?? [];
  const cloudflare = useQuery({ queryKey: ["cloudflare"], queryFn: api.cloudflare });
  const [base, setBase] = useState(NONE);
  const [sub, setSub] = useState("");

  // Split the value once the list is known, and again when it changes from
  // outside (a reset); the fields' own edits don't re-split, so "Other"
  // doesn't snap back to a listed base.
  const [ready, setReady] = useState(false);
  const emitted = useRef<string | null>(null);
  useEffect(() => {
    if (!domains.data || (ready && value === emitted.current)) return;
    const s = split(value, bases);
    setBase(s.base);
    setSub(s.sub);
    emitted.current = value;
    setReady(true);
  }, [domains.data, value]);

  const emit = (domain: string) => {
    emitted.current = domain;
    onChange(domain);
  };
  const update = (nextBase: string, nextSub: string) => {
    setBase(nextBase);
    setSub(nextSub);
    const s = nextSub.trim().toLowerCase();
    if (nextBase === NONE) emit("");
    else if (nextBase === OTHER) emit(s);
    else emit(s ? `${s}.${nextBase}` : nextBase);
  };

  // What the text field should hold after switching to base b.
  const subFor = (b: string) => {
    if (b === OTHER) return value;
    if (base !== OTHER) return sub;
    const s = split(value, [b]); // keep "app" when app.example.com matches b
    return s.base === b ? s.sub : "";
  };

  const noList = domains.isSuccess && bases.length === 0;
  const listed = domains.data?.find((d) => d.name === base);
  const gapZone = listed?.cloudflare && listed.proxied ? edgeCertGap(host(value), cloudflare.data?.zones ?? []) : undefined;
  if (noList || !ready) {
    return (
      <FloatingInput
        label={label}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="app.example.com"
        className={className}
        description={
          <>
            {value && note ? note : (emptyText ?? "Empty for a private service, reachable only inside the project.")}{" "}
            {noList && (
              <>
                List your domains in{" "}
                <Link to="/domains" className="underline underline-offset-2">
                  Domains & DNS
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
          label={label}
          value={base}
          onValueChange={(b) => update(b, subFor(b))}
          className={base === NONE ? "sm:col-span-2" : undefined}
          options={[
            { value: NONE, label: emptyLabel },
            ...bases.map((b) => ({ value: b, label: b })),
            { value: OTHER, label: "Other domain…" },
          ]}
        />
      </div>
      {gapZone && (
        <p className="px-1 text-xs text-amber-600 dark:text-amber-400">
          Cloudflare's free certificate only covers <span className="font-mono">*.{gapZone}</span>: through the proxy,{" "}
          <span className="font-mono">{host(value)}</span> gets a TLS error. Prefer a single level like{" "}
          <span className="font-mono">{flat(host(value), gapZone)}</span>.
        </p>
      )}
      <p className="px-1 text-xs text-muted-foreground">
        {value && note ? (
          note
        ) : value ? (
          <>
            Served at <span className="font-mono">https://{value}</span>
          </>
        ) : (
          (emptyText ?? "Private: reachable only inside the project.")
        )}
      </p>
    </div>
  );
}
