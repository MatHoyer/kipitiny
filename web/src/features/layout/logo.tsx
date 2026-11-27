import type { Branding } from "@/api";
import { defaultBrandName, useBranding } from "@/lib/branding";
import { cn } from "@/lib/utils";

// Same mark and wordmark as the landing page (kipitiny-homepage repo), unless
// an admin set the manager's own name and logo (Settings › Appearance).
export function Logo({ className, branding }: { className?: string; /** Shown instead of the saved one, e.g. a preview. */ branding?: Branding }) {
  const saved = useBranding();
  const b = branding ?? saved;
  return (
    <span className={cn("inline-flex min-w-0 items-center gap-2 font-display font-bold tracking-[-0.02em]", className)}>
      {b.logo ? <img src={b.logo} alt="" className="size-[1.15em] shrink-0 object-contain" /> : <LogoMark className="size-[1.15em]" />}
      <span className="truncate">{b.name || defaultBrandName}</span>
    </span>
  );
}

/** The mark alone: an outlined square with the yellow block. */
export function LogoMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 26 26" aria-hidden="true" className={cn("shrink-0", className)}>
      <rect x="1" y="1" width="24" height="24" rx="5" fill="none" stroke="currentColor" strokeWidth="2" />
      <rect x="6" y="15" width="5" height="5" rx="1" fill="#f5c518" />
    </svg>
  );
}
