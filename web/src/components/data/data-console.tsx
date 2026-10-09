import { useMutation, useQuery } from "@tanstack/react-query";
import { CircleCheck, History, Lock, LockOpen, Play, TriangleAlert } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { CopyButton, Loading } from "@/components/common";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { friendlyError } from "@/lib/errors";
import { cn } from "@/lib/utils";
import { api, ApiError, type ConsoleResult } from "../../api";
import { DataTable } from "./data-table";

// The editor is its own chunk. Not React.lazy: lazy suspends on its first
// render even when the chunk is already loaded, which flashes the fallback.
type EditorModule = typeof import("./sql-editor");
let editorModule: EditorModule | null = null;
let editorLoading: Promise<EditorModule> | null = null;
const loadSqlEditor = () => (editorLoading ??= import("./sql-editor").then((m) => (editorModule = m)));
/** Fetches the editor's chunk ahead of time, so opening the console shows it at once. */
export const preloadSqlEditor = () => void loadSqlEditor();

function useSqlEditor() {
  const [mod, setMod] = useState(editorModule);
  useEffect(() => {
    if (!mod) loadSqlEditor().then(setMod);
  }, [mod]);
  return mod?.default;
}

const HISTORY_MAX = 30;

/** Past queries per service, in this browser only. */
function useHistory(serviceId: string) {
  const key = `kipitiny.console.${serviceId}`;
  const [items, setItems] = useState<string[]>(() => {
    try {
      return JSON.parse(localStorage.getItem(key) ?? "[]");
    } catch {
      return [];
    }
  });
  const push = (q: string) => {
    const next = [q, ...items.filter((i) => i !== q)].slice(0, HISTORY_MAX);
    setItems(next);
    try {
      localStorage.setItem(key, JSON.stringify(next));
    } catch {
      // Private mode or full storage: history just doesn't persist.
    }
  };
  return { items, push };
}

/** Read only, or writes allowed after a confirmation. */
function WriteMode({ write, onChange }: { write: boolean; onChange: (w: boolean) => void }) {
  const [asking, setAsking] = useState(false);
  return (
    <>
      <ToggleGroup
        type="single"
        size="sm"
        variant="outline"
        spacing={0}
        value={write ? "write" : "read"}
        onValueChange={(v) => (v === "write" ? setAsking(true) : v === "read" && onChange(false))}
        aria-label="Write mode"
      >
        <ToggleGroupItem value="read" className="text-muted-foreground data-[state=on]:text-foreground">
          <Lock /> Read only
        </ToggleGroupItem>
        <ToggleGroupItem value="write" className="text-muted-foreground data-[state=on]:bg-destructive/10 data-[state=on]:text-destructive">
          <LockOpen /> Allow writes
        </ToggleGroupItem>
      </ToggleGroup>
      <AlertDialog open={asking} onOpenChange={setAsking}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogMedia className="bg-destructive/10 text-destructive">
              <TriangleAlert />
            </AlertDialogMedia>
            <AlertDialogTitle>Allow writes?</AlertDialogTitle>
            <AlertDialogDescription>
              What you run then changes the database right away. Only a restore from a backup undoes it.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Stay read only</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={() => onChange(true)}>
              Allow writes
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}

/** A database's own error stays verbatim (psql points with LINE and ^); others read as sentences. */
const consoleError = (err: unknown) => (err instanceof ApiError && err.status === 400 ? err.message : friendlyError(err));

const elapsed = (ms: number) => (ms < 1000 ? `${Math.round(ms)} ms` : `${(ms / 1000).toFixed(1)} s`);

/** SQL console: an editor that knows the schema, results as a typed grid. */
export function PgConsole({ serviceId }: { serviceId: string }) {
  const [query, setQuery] = useState("");
  const [write, setWrite] = useState(false);
  const hist = useHistory(serviceId);
  const tables = useQuery({ queryKey: ["data", serviceId, "tables"], queryFn: () => api.pgTables(serviceId) });
  const schema = useMemo(() => {
    const ns: Record<string, Record<string, string[]>> = {};
    for (const tb of tables.data ?? []) (ns[tb.schema] ??= {})[tb.name] = tb.columns.map((c) => c.name);
    return ns;
  }, [tables.data]);
  const run = useMutation({
    meta: { error: false },
    mutationFn: async (v: { query: string; write: boolean }) => {
      const t0 = performance.now();
      const res = await api.dataConsole(serviceId, v.query, v.write);
      return { res, ms: performance.now() - t0 };
    },
  });
  const SqlEditor = useSqlEditor();
  const placeholder = tables.data?.[0] ? `SELECT * FROM ${tables.data[0].name} LIMIT 20` : "SELECT now()";
  const submit = () => {
    const q = query.trim();
    if (!q || run.isPending) return;
    hist.push(q);
    run.mutate({ query: q, write });
  };

  return (
    <div className="space-y-4">
      <div className={cn("overflow-hidden rounded-lg border bg-card transition-colors", write && "border-destructive")}>
        {SqlEditor ? (
          <SqlEditor value={query} onChange={setQuery} onRun={submit} schema={schema} label="SQL" placeholder={placeholder} />
        ) : (
          // Until the editor loads, the same box with the same placeholder: nothing moves.
          <div className="min-h-[7.5rem] px-4 py-3 font-mono text-[0.85rem] leading-[1.4] text-muted-foreground">{placeholder}</div>
        )}
        <div className="flex flex-wrap items-center gap-2 border-t bg-muted/40 px-2 py-2">
          <WriteMode write={write} onChange={setWrite} />
          <QueryHistory items={hist.items} onPick={setQuery} />
          <span className="ml-auto hidden text-xs text-muted-foreground sm:inline">
            {write ? "Runs as one transaction" : "Session is read only"}
          </span>
          <Button size="sm" variant={write ? "destructive" : "default"} loading={run.isPending} disabled={!query.trim()} onClick={submit}>
            <Play /> Run
            <kbd className="ml-1 hidden font-sans text-[0.7rem] opacity-60 sm:inline">Ctrl ↵</kbd>
          </Button>
        </div>
      </div>
      {run.error && <ConsoleError message={consoleError(run.error)} />}
      {run.data && <PgResult res={run.data.res} ms={run.data.ms} />}
    </div>
  );
}

function QueryHistory({ items, onPick }: { items: string[]; onPick: (q: string) => void }) {
  if (items.length === 0) return null;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="sm">
          <History /> History
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-[min(32rem,90vw)]">
        <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">Recent, in this browser</DropdownMenuLabel>
        {items.map((q) => (
          <DropdownMenuItem key={q} onSelect={() => onPick(q)} className="block truncate font-mono text-xs">
            {q.replace(/\s+/g, " ")}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/** A database error with its own layout kept: psql points at the spot with LINE and ^. */
function ConsoleError({ message }: { message: string }) {
  return (
    <div role="alert" className="rounded-lg border border-destructive/30 bg-destructive/5 p-3">
      <pre className="font-mono text-[0.8rem] whitespace-pre-wrap text-destructive">{message}</pre>
    </div>
  );
}

function PgResult({ res, ms }: { res: ConsoleResult; ms: number }) {
  const columns = useMemo(() => (res.columns ?? []).map((name) => ({ name })), [res]);
  if (!res.columns) {
    return (
      <p className="flex items-center gap-2 text-sm">
        <CircleCheck className="size-4 text-emerald-500" />
        <span className="font-mono">{res.output}</span>
        <span className="text-muted-foreground">in {elapsed(ms)}</span>
      </p>
    );
  }
  const rows = res.rows ?? [];
  const asCSV = () =>
    [res.columns!, ...rows].map((r) => r.map((v) => (v === null ? "" : /[",\n]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v)).join(",")).join("\n");
  const asJSON = () => JSON.stringify(rows.map((r) => Object.fromEntries(res.columns!.map((c, j) => [c, r[j]]))), null, 2);

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
        <CircleCheck className="size-4 text-emerald-500" />
        <span>
          {rows.length} {rows.length === 1 ? "row" : "rows"}
          {res.more && ", more were cut"}
        </span>
        <span className="text-muted-foreground">in {elapsed(ms)}</span>
        <span className="ml-auto flex items-center gap-1 text-xs text-muted-foreground">
          CSV <CopyButton value={asCSV()} label="Copy as CSV" />
          JSON <CopyButton value={asJSON()} label="Copy as JSON" />
        </span>
      </div>
      <DataTable columns={columns} rows={rows} truncated={res.truncated} empty="The query returned no rows." />
      {res.more && <p className="text-xs text-muted-foreground">Results stop at 200 rows. Add a LIMIT, or export the table from Browse.</p>}
    </div>
  );
}

type Entry = { id: number; cmd: string; write: boolean; output?: string; error?: string; more?: boolean; ms?: number };

const starters = ["INFO keyspace", "DBSIZE", "SCAN 0 COUNT 20", "MEMORY STATS", "SLOWLOG GET 5"];

/** Redis console: a transcript like redis-cli, with ↑/↓ through past commands. */
export function RedisConsole({ serviceId }: { serviceId: string }) {
  const [line, setLine] = useState("");
  const [write, setWrite] = useState(false);
  const [entries, setEntries] = useState<Entry[]>([]);
  const [busy, setBusy] = useState(false);
  const hist = useHistory(serviceId);
  const [cursor, setCursor] = useState(-1);
  const input = useRef<HTMLInputElement>(null);
  const end = useRef<HTMLDivElement>(null);
  const seq = useRef(0);

  useEffect(() => {
    end.current?.scrollIntoView({ block: "nearest" });
  }, [entries]);

  const runLine = async (cmd: string) => {
    cmd = cmd.trim();
    if (!cmd || busy) return;
    hist.push(cmd);
    setLine("");
    setCursor(-1);
    const id = ++seq.current;
    setEntries((e) => [...e, { id, cmd, write }]);
    setBusy(true);
    const t0 = performance.now();
    try {
      const res = await api.dataConsole(serviceId, cmd, write);
      setEntries((e) => e.map((x) => (x.id === id ? { ...x, output: res.output ?? "", more: res.more, ms: performance.now() - t0 } : x)));
    } catch (err) {
      setEntries((e) => e.map((x) => (x.id === id ? { ...x, error: consoleError(err), ms: performance.now() - t0 } : x)));
    } finally {
      setBusy(false);
      input.current?.focus();
    }
  };
  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowUp" || e.key === "ArrowDown") {
      e.preventDefault();
      const next = Math.min(hist.items.length - 1, Math.max(-1, cursor + (e.key === "ArrowUp" ? 1 : -1)));
      setCursor(next);
      setLine(next < 0 ? "" : hist.items[next]);
    } else if (e.key === "l" && e.ctrlKey) {
      e.preventDefault();
      setEntries([]);
    }
  };

  return (
    <div>
      <div
        className={cn("overflow-hidden rounded-lg border bg-card font-mono text-[0.8rem]", write && "border-destructive")}
        onClick={(e) => e.target === e.currentTarget && input.current?.focus()}
      >
        <div className="max-h-[55vh] space-y-3 overflow-y-auto p-3" aria-live="polite">
          {entries.length === 0 && (
            <div className="space-y-2 font-sans text-sm text-muted-foreground">
              <p>Type a command as you would in redis-cli. Try one of these:</p>
              <div className="flex flex-wrap gap-1.5">
                {starters.map((s) => (
                  <button key={s} type="button" onClick={() => runLine(s)} className="rounded-md border bg-background px-2 py-0.5 font-mono text-xs text-foreground hover:border-(--db)/50">
                    {s}
                  </button>
                ))}
              </div>
            </div>
          )}
          {entries.map((e) => (
            <div key={e.id}>
              <p className="flex items-baseline gap-2">
                <span className={cn("select-none", e.write ? "text-destructive" : "text-muted-foreground")}>›</span>
                <span className="flex-1 break-all">{e.cmd}</span>
                {e.ms !== undefined && <span className="font-sans text-xs text-muted-foreground">{elapsed(e.ms)}</span>}
              </p>
              {e.error !== undefined ? (
                <pre className="mt-1 pl-4 whitespace-pre-wrap text-destructive">{e.error}</pre>
              ) : e.output !== undefined ? (
                <pre className="mt-1 pl-4 break-all whitespace-pre-wrap text-muted-foreground">
                  {e.output || "(empty)"}
                  {e.more && "\n… output cut at 1 MiB"}
                </pre>
              ) : (
                <Loading className="justify-start py-1 pl-4" />
              )}
            </div>
          ))}
          <div ref={end} />
        </div>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            runLine(line);
          }}
        >
          <div className="flex items-center gap-2 border-t px-3 py-2">
            <span className={cn("select-none", write ? "text-destructive" : "text-muted-foreground")}>›</span>
            <input
              ref={input}
              autoFocus
              aria-label="Redis command"
              spellCheck={false}
              autoComplete="off"
              value={line}
              onChange={(e) => setLine(e.target.value)}
              onKeyDown={onKey}
              placeholder={busy ? "Running…" : "GET user:42"}
              className="flex-1 bg-transparent outline-none placeholder:text-muted-foreground/60"
            />
            <span className="hidden font-sans text-xs text-muted-foreground sm:inline">↑ history, Ctrl+L clears</span>
          </div>
          <div className="flex flex-wrap items-center gap-2 border-t bg-muted/40 px-2 py-2 font-sans">
            <WriteMode write={write} onChange={setWrite} />
            <span className="ml-auto hidden text-xs text-muted-foreground sm:inline">
              {write ? "Every command runs, except ones that block" : "Commands that change data are refused"}
            </span>
            <Button type="submit" size="sm" variant={write ? "destructive" : "default"} loading={busy} disabled={!line.trim()}>
              <Play /> Run
              <kbd className="ml-1 hidden font-sans text-[0.7rem] opacity-60 sm:inline">↵</kbd>
            </Button>
          </div>
        </form>
      </div>
    </div>
  );
}
