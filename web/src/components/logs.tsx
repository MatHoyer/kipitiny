import { ArrowDown, Download, Eraser, Pause, Play, Search, WrapText } from "lucide-react";
import { Fragment, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { levelRank, logLevel, parseAnsi, stripAnsi, type Level, type Segment } from "@/lib/logs";
import { cn } from "@/lib/utils";
import { api, type LogLine } from "../api";

export const logBox =
  "overflow-auto rounded-lg bg-neutral-950 py-2 font-mono text-xs leading-relaxed text-neutral-200 dark:ring-1 dark:ring-foreground/10";

const lineTone: Record<Level | "none", string> = {
  error: "border-red-500 bg-red-500/10 text-red-200",
  warn: "border-amber-400 bg-amber-400/10 text-amber-100",
  info: "border-transparent",
  debug: "border-transparent text-neutral-500",
  none: "border-transparent",
};

/** A log line's text: its ANSI colours, with the matches of query marked. */
export function LogText({ segments, query }: { segments: Segment[]; query?: string }) {
  return segments.map((s, i) => {
    const style =
      s.fg || s.bg || s.bold || s.dim || s.italic || s.underline
        ? {
            color: s.fg,
            backgroundColor: s.bg,
            fontWeight: s.bold ? 600 : undefined,
            opacity: s.dim ? 0.6 : undefined,
            fontStyle: s.italic ? "italic" : undefined,
            textDecoration: s.underline ? "underline" : undefined,
          }
        : undefined;
    const text = query ? mark(s.text, query) : s.text;
    return style ? (
      <span key={i} style={style}>
        {text}
      </span>
    ) : (
      <Fragment key={i}>{text}</Fragment>
    );
  });
}

function mark(text: string, query: string): ReactNode {
  const lower = text.toLowerCase();
  const out: ReactNode[] = [];
  let from = 0;
  for (let at = lower.indexOf(query); at !== -1; at = lower.indexOf(query, from)) {
    out.push(text.slice(from, at), <mark key={at} className="rounded-sm bg-yellow-300/80 text-neutral-950">{text.slice(at, at + query.length)}</mark>);
    from = at + query.length;
  }
  if (out.length === 0) return text;
  out.push(text.slice(from));
  return out;
}

/** Keeps a scroll box pinned to its bottom until the user scrolls up. */
function useStickToBottom<T extends HTMLElement>(dep: unknown) {
  const ref = useRef<T>(null);
  const [atBottom, setAtBottom] = useState(true);
  const stick = useRef(true);
  const lastTop = useRef(0);
  useEffect(() => {
    const el = ref.current;
    if (stick.current && el) {
      el.scrollTop = el.scrollHeight;
      lastTop.current = el.scrollTop;
    }
  }, [dep]);
  return {
    ref,
    atBottom,
    onScroll: (e: React.UIEvent<T>) => {
      const el = e.currentTarget;
      // Only scrolling up unsticks: lines landing before the scroll event
      // fires mustn't look like the user left the bottom.
      if (el.scrollHeight - el.scrollTop - el.clientHeight < 20) stick.current = true;
      else if (el.scrollTop < lastTop.current) stick.current = false;
      lastTop.current = el.scrollTop;
      setAtBottom(stick.current);
    },
    toBottom: () => {
      stick.current = true;
      setAtBottom(true);
      ref.current?.scrollTo({ top: ref.current.scrollHeight, behavior: "smooth" });
    },
  };
}

function JumpToLatest({ onClick, label = "Latest" }: { onClick: () => void; label?: string }) {
  return (
    <Button size="xs" variant="secondary" onClick={onClick} className="absolute right-4 bottom-3 shadow-md">
      <ArrowDown data-icon="inline-start" />
      {label}
    </Button>
  );
}

/** The output of a deployment: its own step lines, the build's and the containers'. */
export function DeploymentLogView({ text, pending, className }: { text?: string; pending?: boolean; className?: string }) {
  const lines = useMemo(
    () =>
      (text ?? "")
        .replace(/\n$/, "")
        .split("\n")
        .map((raw) => {
          const plain = stripAnsi(raw);
          // Step lines are "[HH:MM:SS] message", in UTC.
          const step = /^\[(\d{2}:\d{2}:\d{2})\] (.*)$/s.exec(plain);
          let tone: Level | "none" | "success" = logLevel(plain) ?? "none";
          if (step?.[2].startsWith("Deployment failed")) tone = "error";
          else if (step?.[2].startsWith("Deployment succeeded")) tone = "success";
          return { raw, step, tone, header: plain.startsWith("--- ") && plain.endsWith(" ---") };
        }),
    [text],
  );
  const scroll = useStickToBottom<HTMLDivElement>(text);
  return (
    <div className="relative">
      <div ref={scroll.ref} onScroll={scroll.onScroll} className={cn(logBox, className)}>
        {pending ? (
          <span className="flex items-center gap-2 px-3 text-neutral-500">
            <Spinner className="size-3.5" />
            Loading log
          </span>
        ) : !text ? (
          <span className="px-3 text-neutral-500">No output.</span>
        ) : (
          lines.map((l, i) => (
            <div
              key={i}
              className={cn(
                "border-l-2 pr-3 pl-2.5 break-words whitespace-pre-wrap",
                l.tone === "success" ? "border-emerald-500 bg-emerald-500/10 text-emerald-200" : lineTone[l.tone],
                l.header && "mt-2 font-semibold text-neutral-400",
              )}
            >
              {l.step ? (
                <>
                  <span className="text-neutral-500 select-none">{l.step[1]} </span>
                  <span className={cn(l.tone === "none" && "text-sky-200")}>{l.step[2]}</span>
                </>
              ) : (
                <LogText segments={parseAnsi(l.raw)} />
              )}
            </div>
          ))
        )}
      </div>
      {!scroll.atBottom && <JumpToLatest onClick={scroll.toBottom} />}
    </div>
  );
}

const MAX_LINES = 2000;

// continued: the line has no level of its own, it carries on a stack trace.
type Entry = LogLine & { id: number; plain: string; segments: Segment[]; level?: Level; continued?: boolean };

type LevelFilter = "all" | Level;

// Local HH:MM:SS; lines Docker didn't timestamp come with the zero time.
function logTime(t: string) {
  const d = new Date(t);
  return d.getFullYear() > 1 ? d.toLocaleTimeString([], { hour12: false }) : "--:--:--";
}

const replicaColors = ["#7dd3fc", "#c4b5fd", "#f9a8d4", "#86efac", "#fdba74", "#5eead4"];

function replicaColor(name: string) {
  let h = 0;
  for (const c of name) h = (h * 31 + c.charCodeAt(0)) | 0;
  return replicaColors[Math.abs(h) % replicaColors.length];
}

function load(key: string, fallback: boolean) {
  try {
    const v = localStorage.getItem(key);
    return v === null ? fallback : v === "1";
  } catch {
    return fallback;
  }
}

function save(key: string, v: boolean) {
  try {
    localStorage.setItem(key, v ? "1" : "0");
  } catch {
    // Private mode: the setting lasts for the page.
  }
}

/**
 * The live logs of a service's containers: coloured by level and by their own
 * ANSI codes, with filters, pause and download.
 */
export function LiveLogs({ serviceId, name, className }: { serviceId: string; name: string; className?: string }) {
  const [lines, setLines] = useState<Entry[]>([]);
  const [ended, setEnded] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [query, setQuery] = useState("");
  const [level, setLevel] = useState<LevelFilter>("all");
  const [replica, setReplica] = useState("all");
  const [paused, setPaused] = useState<Entry[] | null>(null);
  const [wrap, setWrap] = useState(() => load("kipitiny.logs.wrap", true));

  useEffect(() => {
    setLines([]);
    setEnded(false);
    let next = 0;
    // Level of each container's last line, for the indented lines of a stack trace.
    const prev = new Map<string, Level | undefined>();
    let buffer: Entry[] = [];
    let timer: ReturnType<typeof setTimeout> | undefined;
    const flush = () => {
      timer = undefined;
      const batch = buffer;
      buffer = [];
      setLines((old) => {
        const all = old.concat(batch);
        return all.length > MAX_LINES ? all.slice(-MAX_LINES) : all;
      });
    };

    const es = new EventSource(api.logsUrl(serviceId));
    es.onmessage = (e) => {
      const line = JSON.parse(e.data) as LogLine;
      const plain = stripAnsi(line.text);
      let level = logLevel(plain);
      const continued = !level && /^(\s|at |Caused by)/.test(plain);
      if (continued) level = prev.get(line.container);
      prev.set(line.container, level);
      buffer.push({ ...line, id: next++, plain, segments: parseAnsi(line.text), level, continued });
      if (buffer.length > MAX_LINES) buffer = buffer.slice(-MAX_LINES);
      timer ??= setTimeout(flush, 50);
    };
    const end = () => {
      es.close();
      if (timer) flush();
      clearTimeout(timer);
      setEnded(true);
    };
    // The server sends "end" when every container stream closed.
    es.addEventListener("end", end);
    es.onerror = end;
    return () => {
      es.close();
      clearTimeout(timer);
    };
  }, [serviceId, attempt]);

  const shown = paused ?? lines;
  const replicas = useMemo(() => [...new Set(lines.map((l) => l.container))].sort(), [lines]);
  const multi = replicas.length > 1;
  const q = query.trim().toLowerCase();
  const visible = useMemo(
    () =>
      shown.filter(
        (l) =>
          (level === "all" || (l.level !== undefined && levelRank[l.level] >= levelRank[level])) &&
          (replica === "all" || l.container === replica) &&
          (!q || l.plain.toLowerCase().includes(q)),
      ),
    [shown, level, replica, q],
  );
  const counts = useMemo(() => {
    let error = 0;
    let warn = 0;
    for (const l of shown) {
      if (l.continued) continue;
      if (l.level === "error") error++;
      else if (l.level === "warn") warn++;
    }
    return { error, warn };
  }, [shown]);
  const behind = paused && lines.length > 0 ? lines[lines.length - 1].id - (paused.at(-1)?.id ?? -1) : 0;
  const filtered = visible.length !== shown.length;

  const scroll = useStickToBottom<HTMLDivElement>(visible);

  const download = () => {
    const text = visible.map((l) => `${l.time} ${multi ? `${l.container} ` : ""}${l.plain}`).join("\n") + "\n";
    const a = document.createElement("a");
    a.href = URL.createObjectURL(new Blob([text], { type: "text/plain" }));
    a.download = `${name}-${new Date().toISOString().slice(0, 19).replaceAll(":", "")}.log`;
    a.click();
    URL.revokeObjectURL(a.href);
  };

  return (
    <section className={cn("space-y-3", className)}>
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-48 flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => e.key === "Escape" && setQuery("")}
            placeholder="Filter lines"
            aria-label="Filter lines"
            className="h-8 pl-9"
          />
        </div>
        <Select value={level} onValueChange={(v) => setLevel(v as LevelFilter)}>
          <SelectTrigger size="sm" aria-label="Level" className="min-w-36">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All lines</SelectItem>
            <SelectItem value="info">Info and above</SelectItem>
            <SelectItem value="warn">Warnings and errors</SelectItem>
            <SelectItem value="error">Errors only</SelectItem>
          </SelectContent>
        </Select>
        {multi && (
          <Select value={replica} onValueChange={setReplica}>
            <SelectTrigger size="sm" aria-label="Replica" className="min-w-36">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All replicas</SelectItem>
              {replicas.map((r) => (
                <SelectItem key={r} value={r}>
                  {r}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
        <div className="flex items-center gap-1">
          <Button
            variant="ghost"
            size="icon-sm"
            title={paused ? "Resume" : "Pause"}
            onClick={() => setPaused((p) => (p ? null : lines))}
          >
            {paused ? <Play /> : <Pause />}
          </Button>
          <Button
            variant="ghost"
            size="icon-sm"
            title={wrap ? "Don't wrap lines" : "Wrap lines"}
            aria-pressed={wrap}
            className={cn(wrap && "bg-muted")}
            onClick={() => {
              save("kipitiny.logs.wrap", !wrap);
              setWrap(!wrap);
            }}
          >
            <WrapText />
          </Button>
          <Button variant="ghost" size="icon-sm" title="Download shown lines" disabled={visible.length === 0} onClick={download}>
            <Download />
          </Button>
          <Button
            variant="ghost"
            size="icon-sm"
            title="Clear"
            disabled={lines.length === 0}
            onClick={() => {
              setLines([]);
              setPaused(null);
            }}
          >
            <Eraser />
          </Button>
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
        {ended ? (
          <span className="flex items-center gap-2">
            <span className="size-2 rounded-full bg-neutral-400" />
            Stream closed
            <Button variant="link" size="xs" className="h-auto p-0" onClick={() => setAttempt((n) => n + 1)}>
              Reconnect
            </Button>
          </span>
        ) : paused ? (
          <span className="flex items-center gap-2">
            <span className="size-2 rounded-full bg-amber-500" />
            Paused{behind > 0 && ` · ${behind} new line${behind === 1 ? "" : "s"}`}
          </span>
        ) : (
          <span className="flex items-center gap-2">
            <span className="relative flex size-2">
              <span className="absolute inline-flex size-full animate-ping rounded-full bg-emerald-500 opacity-60" />
              <span className="relative inline-flex size-2 rounded-full bg-emerald-500" />
            </span>
            Live
          </span>
        )}
        <span>
          {filtered ? `${visible.length} of ${shown.length}` : shown.length} line{shown.length === 1 ? "" : "s"}
        </span>
        {counts.error > 0 && (
          <button type="button" className="text-red-600 hover:underline dark:text-red-400" onClick={() => setLevel("error")}>
            {counts.error} error{counts.error === 1 ? "" : "s"}
          </button>
        )}
        {counts.warn > 0 && (
          <button type="button" className="text-amber-600 hover:underline dark:text-amber-400" onClick={() => setLevel("warn")}>
            {counts.warn} warning{counts.warn === 1 ? "" : "s"}
          </button>
        )}
        {filtered && (
          <button
            type="button"
            className="hover:text-foreground hover:underline"
            onClick={() => {
              setQuery("");
              setLevel("all");
              setReplica("all");
            }}
          >
            Clear filters
          </button>
        )}
      </div>

      <div className="relative">
        <div ref={scroll.ref} onScroll={scroll.onScroll} className={cn(logBox, "h-[calc(100svh-26rem)] min-h-80")}>
          {visible.length === 0 ? (
            <span className="flex items-center gap-2 px-3 text-neutral-500">
              {shown.length > 0 ? (
                "No line matches the filters."
              ) : ended ? (
                "Stream closed."
              ) : (
                <>
                  <Spinner className="size-3.5" />
                  Waiting for logs
                </>
              )}
            </span>
          ) : (
            <div className={cn(!wrap && "w-max min-w-full")}>
              {visible.map((l) => (
                <div key={l.id} className={cn("flex gap-3 border-l-2 pr-3 pl-2.5", lineTone[l.level ?? "none"])}>
                  <span className="shrink-0 text-neutral-500 select-none" title={l.time}>
                    {logTime(l.time)}
                  </span>
                  {multi && (
                    <span className="shrink-0 select-none" style={{ color: replicaColor(l.container) }}>
                      {l.container}
                    </span>
                  )}
                  <span className={cn("min-w-0", wrap ? "break-words whitespace-pre-wrap" : "whitespace-pre")}>
                    <LogText segments={l.segments} query={q} />
                  </span>
                </div>
              ))}
            </div>
          )}
        </div>
        {!scroll.atBottom && <JumpToLatest onClick={scroll.toBottom} />}
      </div>
    </section>
  );
}
