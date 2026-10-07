import { cn } from "@/lib/utils";

// Same mark and wordmark as the landing page (kipitiny-homepage repo).
export function Logo({ className }: { className?: string }) {
  return (
    <span className={cn("inline-flex items-center gap-2 font-display font-bold tracking-[-0.02em]", className)}>
      <svg viewBox="0 0 26 26" aria-hidden="true" className="size-[1.15em] shrink-0">
        <rect x="1" y="1" width="24" height="24" rx="5" fill="none" stroke="currentColor" strokeWidth="2" />
        <rect x="6" y="15" width="5" height="5" rx="1" fill="#f5c518" />
      </svg>
      kipitiny
    </span>
  );
}
