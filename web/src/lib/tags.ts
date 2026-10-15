/** The palette tags take their color from, as the server's (core.TagColors). */
export const tagColors = [
  "red",
  "orange",
  "amber",
  "yellow",
  "lime",
  "green",
  "emerald",
  "teal",
  "cyan",
  "sky",
  "blue",
  "indigo",
  "violet",
  "purple",
  "fuchsia",
  "pink",
  "rose",
  "gray",
] as const;

/**
 * The color of a tag nobody picked one for, as the server derives it
 * (core.AutoTagColor): FNV-1a of the name's UTF-8 bytes over the palette
 * without gray.
 */
export function autoTagColor(name: string): string {
  let h = 0x811c9dc5;
  for (const b of new TextEncoder().encode(name)) {
    h ^= b;
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return tagColors[h % (tagColors.length - 1)];
}

/** Lowercased, spaces collapsed: how the server stores a tag. */
export const normalizeTag = (t: string) => t.trim().split(/\s+/).join(" ").toLowerCase();

// Written out whole so Tailwind finds every class.
const chips: Record<string, string> = {
  red: "bg-red-500/12 text-red-700 ring-red-500/25 dark:text-red-300",
  orange: "bg-orange-500/12 text-orange-700 ring-orange-500/25 dark:text-orange-300",
  amber: "bg-amber-500/12 text-amber-700 ring-amber-500/25 dark:text-amber-300",
  yellow: "bg-yellow-500/15 text-yellow-800 ring-yellow-500/30 dark:text-yellow-300",
  lime: "bg-lime-500/15 text-lime-800 ring-lime-500/30 dark:text-lime-300",
  green: "bg-green-500/12 text-green-700 ring-green-500/25 dark:text-green-300",
  emerald: "bg-emerald-500/12 text-emerald-700 ring-emerald-500/25 dark:text-emerald-300",
  teal: "bg-teal-500/12 text-teal-700 ring-teal-500/25 dark:text-teal-300",
  cyan: "bg-cyan-500/12 text-cyan-700 ring-cyan-500/25 dark:text-cyan-300",
  sky: "bg-sky-500/12 text-sky-700 ring-sky-500/25 dark:text-sky-300",
  blue: "bg-blue-500/12 text-blue-700 ring-blue-500/25 dark:text-blue-300",
  indigo: "bg-indigo-500/12 text-indigo-700 ring-indigo-500/25 dark:text-indigo-300",
  violet: "bg-violet-500/12 text-violet-700 ring-violet-500/25 dark:text-violet-300",
  purple: "bg-purple-500/12 text-purple-700 ring-purple-500/25 dark:text-purple-300",
  fuchsia: "bg-fuchsia-500/12 text-fuchsia-700 ring-fuchsia-500/25 dark:text-fuchsia-300",
  pink: "bg-pink-500/12 text-pink-700 ring-pink-500/25 dark:text-pink-300",
  rose: "bg-rose-500/12 text-rose-700 ring-rose-500/25 dark:text-rose-300",
  gray: "bg-zinc-500/12 text-zinc-700 ring-zinc-500/25 dark:text-zinc-300",
};

const dots: Record<string, string> = {
  red: "bg-red-500",
  orange: "bg-orange-500",
  amber: "bg-amber-500",
  yellow: "bg-yellow-400",
  lime: "bg-lime-500",
  green: "bg-green-500",
  emerald: "bg-emerald-500",
  teal: "bg-teal-500",
  cyan: "bg-cyan-500",
  sky: "bg-sky-500",
  blue: "bg-blue-500",
  indigo: "bg-indigo-500",
  violet: "bg-violet-500",
  purple: "bg-purple-500",
  fuchsia: "bg-fuchsia-500",
  pink: "bg-pink-500",
  rose: "bg-rose-500",
  gray: "bg-zinc-500",
};

export const tagChipClass = (color: string) => chips[color] ?? chips.gray;
export const tagDotClass = (color: string) => dots[color] ?? dots.gray;
