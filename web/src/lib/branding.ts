import { useQuery } from "@tanstack/react-query";
import { useEffect } from "react";
import { api, type Branding } from "@/api";

export const defaultBrandName = "kipitiny";

/** The manager's branding, from the auth state every page already has. */
export function useBranding(): Branding {
  const auth = useQuery({ queryKey: ["auth"], queryFn: api.authState, staleTime: Infinity });
  return auth.data?.branding ?? {};
}

const accentVars = ["--primary", "--ring", "--sidebar-primary", "--sidebar-ring"];
const foregroundVars = ["--primary-foreground", "--sidebar-primary-foreground"];

/** Applies the branding to the page: accent colour, tab title and favicon. */
export function useApplyBranding(b: Branding) {
  useEffect(() => {
    const root = document.documentElement.style;
    // Inline variables win over :root and .dark alike: one accent for both themes.
    for (const v of accentVars) b.accent ? root.setProperty(v, b.accent) : root.removeProperty(v);
    for (const v of foregroundVars) b.accent ? root.setProperty(v, readableOn(b.accent)) : root.removeProperty(v);
  }, [b.accent]);

  useEffect(() => {
    document.title = b.name || defaultBrandName;
  }, [b.name]);

  useEffect(() => {
    const icon = document.querySelector<HTMLLinkElement>("link[rel=icon]");
    if (!icon) return;
    icon.dataset.default ??= icon.href;
    icon.href = b.logo || icon.dataset.default;
    if (b.logo) icon.removeAttribute("type");
    else icon.type = "image/svg+xml";
  }, [b.logo]);
}

/** Black or white, whichever reads better on hex (#rrggbb), by WCAG contrast. */
export function readableOn(hex: string): string {
  const [r, g, b] = [1, 3, 5].map((i) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255;
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  const l = 0.2126 * r + 0.7152 * g + 0.0722 * b;
  return (l + 0.05) / 0.05 > 1.05 / (l + 0.05) ? "#18181b" : "#ffffff";
}
