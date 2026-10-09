// Log line parsing: ANSI colours into styled segments, and a best guess at
// each line's level from the common formats (JSON, logfmt, nginx, Postgres,
// Redis, klog, access logs).

export type Level = "error" | "warn" | "info" | "debug";

export type Segment = {
  text: string;
  fg?: string;
  bg?: string;
  bold?: boolean;
  dim?: boolean;
  italic?: boolean;
  underline?: boolean;
};

// Tuned for the dark log box (neutral-950).
const palette = [
  "#525252", "#f87171", "#4ade80", "#facc15", "#60a5fa", "#e879f9", "#22d3ee", "#d4d4d4",
  "#737373", "#fca5a5", "#86efac", "#fde047", "#93c5fd", "#f0abfc", "#67e8f9", "#fafafa",
];

function color256(n: number): string | undefined {
  if (n < 16) return palette[n];
  if (n < 232) {
    const v = [0, 95, 135, 175, 215, 255];
    const i = n - 16;
    return `rgb(${v[Math.floor(i / 36)]},${v[Math.floor(i / 6) % 6]},${v[i % 6]})`;
  }
  if (n < 256) {
    const g = 8 + (n - 232) * 10;
    return `rgb(${g},${g},${g})`;
  }
}

// Escape sequences other than colours (cursor moves, titles, …) are dropped.
const ESCAPE = /\x1b\[([\d;:?]*)([A-Za-z])|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][\dA-Z]|\x1b[=>78]/g;

/** Text with escape sequences removed and only the last carriage-return frame kept. */
export function stripAnsi(text: string): string {
  return lastFrame(text).replace(ESCAPE, "");
}

// Progress bars redraw a line with \r: show what the terminal would end on.
function lastFrame(text: string): string {
  const parts = text.split("\r");
  for (let i = parts.length - 1; i >= 0; i--) if (parts[i] !== "") return parts[i];
  return "";
}

export function parseAnsi(text: string): Segment[] {
  text = lastFrame(text);
  if (!text.includes("\x1b")) return [{ text }];
  const out: Segment[] = [];
  let style: Omit<Segment, "text"> = {};
  let last = 0;
  for (const m of text.matchAll(ESCAPE)) {
    if (m.index > last) out.push({ ...style, text: text.slice(last, m.index) });
    last = m.index + m[0].length;
    if (m[2] === "m") style = applySgr(style, m[1]);
  }
  if (last < text.length) out.push({ ...style, text: text.slice(last) });
  return out;
}

function applySgr(style: Omit<Segment, "text">, params: string): Omit<Segment, "text"> {
  const codes = params === "" ? [0] : params.split(/[;:]/).map(Number);
  const s = { ...style };
  for (let i = 0; i < codes.length; i++) {
    const c = codes[i];
    if (c === 0) Object.keys(s).forEach((k) => delete s[k as keyof typeof s]);
    else if (c === 1) s.bold = true;
    else if (c === 2) s.dim = true;
    else if (c === 3) s.italic = true;
    else if (c === 4) s.underline = true;
    else if (c === 22) s.bold = s.dim = undefined;
    else if (c === 23) s.italic = undefined;
    else if (c === 24) s.underline = undefined;
    else if (c >= 30 && c <= 37) s.fg = palette[c - 30];
    else if (c >= 90 && c <= 97) s.fg = palette[c - 90 + 8];
    else if (c >= 40 && c <= 47) s.bg = palette[c - 40];
    else if (c >= 100 && c <= 107) s.bg = palette[c - 100 + 8];
    else if (c === 39) s.fg = undefined;
    else if (c === 49) s.bg = undefined;
    else if (c === 38 || c === 48) {
      let col: string | undefined;
      if (codes[i + 1] === 5) {
        col = color256(codes[i + 2]);
        i += 2;
      } else if (codes[i + 1] === 2) {
        col = `rgb(${codes[i + 2]},${codes[i + 3]},${codes[i + 4]})`;
        i += 4;
      }
      if (c === 38) s.fg = col;
      else s.bg = col;
    }
  }
  return s;
}

const levelWords: Record<string, Level> = {
  fatal: "error", panic: "error", emerg: "error", emergency: "error", alert: "error",
  crit: "error", critical: "error", error: "error", err: "error", severe: "error",
  warn: "warn", warning: "warn",
  notice: "info", info: "info", information: "info", log: "info",
  debug: "debug", trace: "debug", verbose: "debug", dbg: "debug",
};

const JSON_LEVEL = /"(?:level|lvl|severity|log\.level|levelname)"\s*:\s*"(\w+)"/i;
const LOGFMT_LEVEL = /(?:^|\s)(?:level|lvl|severity)=["']?(\w+)/i;
// Redis: "1:M 09 Oct 2026 10:00:00.000 # Warning…"; # is a warning, . and - are debug.
const REDIS = /^\d+:[XCSM] \d{1,2} \w{3} \d{4} [\d:.]+ ([.\-*#]) /;
// klog/glog: "E1009 10:00:00.000000 …".
const KLOG = /^([EWIF])\d{4} \d{2}:\d{2}:\d{2}/;
const BRACKETED = /[[<(](emerg|alert|crit|critical|error|err|warn|warning|notice|info|debug|trace)[\]>)]/i;
// Upper case only: "no error" in a sentence isn't an error line.
const KEYWORD = /\b(FATAL|PANIC|EMERG|CRIT(?:ICAL)?|ERROR|ERR|WARN(?:ING)?|NOTICE|INFO|DEBUG|TRACE|LOG)\b/;
const HTTP_STATUS = /HTTP\/[\d.]+"\s+([1-5]\d\d)\b/;

/** The level a line was logged at, when its format says. */
export function logLevel(plain: string): Level | undefined {
  let m = JSON_LEVEL.exec(plain) ?? LOGFMT_LEVEL.exec(plain);
  if (m && levelWords[m[1].toLowerCase()]) return levelWords[m[1].toLowerCase()];
  if ((m = REDIS.exec(plain))) return m[1] === "#" ? "warn" : m[1] === "*" ? "info" : "debug";
  if ((m = KLOG.exec(plain))) return ({ E: "error", F: "error", W: "warn", I: "info" } as const)[m[1] as "E"];
  if ((m = BRACKETED.exec(plain) ?? KEYWORD.exec(plain))) return levelWords[m[1].toLowerCase()];
  if ((m = HTTP_STATUS.exec(plain))) return m[1][0] === "5" ? "error" : m[1][0] === "4" ? "warn" : "info";
}

/** How a level-filter setting compares: a line passes when its rank is at least the filter's. */
export const levelRank: Record<Level, number> = { debug: 0, info: 1, warn: 2, error: 3 };
