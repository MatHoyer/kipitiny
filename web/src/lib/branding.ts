import { useQuery } from "@tanstack/react-query";
import { createContext, useEffect } from "react";
import { api, type Branding, type Theme } from "@/api";

export const defaultBrandName = "kipitiny";

/** The manager's branding, from the auth state every page already has. */
export function useBranding(): Branding {
  const auth = useQuery({ queryKey: ["auth"], queryFn: api.authState, staleTime: Infinity });
  return auth.data?.branding ?? {};
}

/** Shows a theme on the whole UI before it's saved (Settings › Appearance); null goes back to the saved one. */
export const BrandingPreview = createContext<(b: Branding | null) => void>(() => {});

/** Applies the branding to the page: theme, tab title, favicon and home-screen icon. */
export function useApplyBranding(b: Branding) {
  const css = themeCSS(b);
  useEffect(() => {
    let style = document.getElementById("kipitiny-theme");
    if (!style) {
      style = document.createElement("style");
      style.id = "kipitiny-theme";
      document.head.append(style); // after index.css: same selectors, so these win
    }
    style.textContent = css;
  }, [css]);

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
    // iOS reads it when the app is added to the home screen
    const touch = document.querySelector<HTMLLinkElement>("link[rel=apple-touch-icon]");
    if (!touch) return;
    touch.dataset.default ??= touch.href;
    touch.href = b.logo || touch.dataset.default;
  }, [b.logo]);
}

const hexRe = /^#[0-9a-f]{6}$/i;
const hex = (v?: string) => (v && hexRe.test(v) ? v : undefined);

export const radii: Record<NonNullable<Theme["radius"]>, string> = {
  none: "0rem",
  small: "0.375rem",
  medium: "0.625rem",
  large: "0.875rem",
  full: "1.25rem",
};

export const fonts: Record<NonNullable<Theme["font"]>, string> = {
  geist: "'Geist Variable', sans-serif",
  system: "ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif",
  serif: "ui-serif, Georgia, Cambria, 'Times New Roman', serif",
  mono: "'Geist Mono Variable', ui-monospace, monospace",
};

/** Tailwind's spacing unit: every padding, gap and size scales with it. */
const spacing: Record<NonNullable<Theme["density"]>, string> = { comfortable: "0.25rem", compact: "0.22rem" };

/**
 * The theme as CSS overriding index.css's :root and .dark variables. Only
 * known values make it in: colours must be #rrggbb, the rest come from the
 * tables above.
 */
export function themeCSS(t: Theme): string {
  // Colours go to light or dark only: a light value left on :root would beat
  // index.css's .dark one (same specificity, later).
  const shared: Record<string, string> = {};
  const light: Record<string, string> = {};
  const dark: Record<string, string> = {};
  const accent = hex(t.accent);
  const accentDark = hex(t.accentDark) ?? accent;
  const setAccent = (vars: Record<string, string>, c: string) => {
    for (const v of ["--primary", "--ring", "--sidebar-primary", "--sidebar-ring"]) vars[v] = c;
    for (const v of ["--primary-foreground", "--sidebar-primary-foreground"]) vars[v] = readableOn(c);
  };
  if (accent) setAccent(light, accent);
  if (accentDark) setAccent(dark, accentDark);

  const tint = hex(t.tint);
  if (tint) {
    const mix = (pct: number, base: string) => `color-mix(in oklab, ${tint} ${pct}%, ${base})`;
    Object.assign(light, {
      "--background": mix(3, "white"),
      "--card": mix(1, "white"),
      "--popover": mix(1, "white"),
      "--sidebar": mix(6, "white"),
      "--muted": mix(8, "oklch(0.97 0 0)"),
      "--secondary": mix(8, "oklch(0.97 0 0)"),
      "--accent": mix(8, "oklch(0.97 0 0)"),
      "--sidebar-accent": mix(10, "oklch(0.97 0 0)"),
      "--border": mix(12, "oklch(0.922 0 0)"),
      "--input": mix(12, "oklch(0.922 0 0)"),
      "--sidebar-border": mix(12, "oklch(0.922 0 0)"),
    });
    Object.assign(dark, {
      "--background": mix(7, "oklch(0.145 0 0)"),
      "--card": mix(8, "oklch(0.205 0 0)"),
      "--popover": mix(8, "oklch(0.205 0 0)"),
      "--sidebar": mix(8, "oklch(0.205 0 0)"),
      "--muted": mix(10, "oklch(0.269 0 0)"),
      "--secondary": mix(10, "oklch(0.269 0 0)"),
      "--accent": mix(10, "oklch(0.269 0 0)"),
      "--sidebar-accent": mix(10, "oklch(0.269 0 0)"),
      "--border": mix(12, "oklch(0.275 0 0)"),
      "--input": mix(12, "oklch(0.31 0 0)"),
      "--sidebar-border": mix(12, "oklch(0.275 0 0)"),
    });
  }

  if (t.radius && t.radius in radii) shared["--radius"] = radii[t.radius];
  if (t.font && t.font in fonts) shared["--app-font"] = fonts[t.font];
  if (t.density && t.density in spacing) shared["--spacing"] = spacing[t.density];

  const block = (sel: string, vars: Record<string, string>) =>
    Object.keys(vars).length ? `${sel} {\n${Object.entries(vars).map(([k, v]) => `  ${k}: ${v};`).join("\n")}\n}\n` : "";
  return block(":root", shared) + block(":root:not(.dark)", light) + block(".dark", dark);
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
