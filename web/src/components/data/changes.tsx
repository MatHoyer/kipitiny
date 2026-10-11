import { useMutation } from "@tanstack/react-query";
import { Save, Undo2 } from "lucide-react";
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useBlocker } from "react-router";
import { Loading } from "@/components/common";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { cn } from "@/lib/utils";
import type { ChangeResult } from "../../api";
import { ConsoleError, consoleError } from "./data-console";

type Unsaved = {
  /** Reports how many changes wait to be saved; 0 once saved or discarded. */
  setDirty: (n: number) => void;
  /** Runs leave at once, or after the user agrees to drop the unsaved changes. */
  confirm: (leave: () => void) => void;
};

const UnsavedContext = createContext<Unsaved>({ setDirty: () => {}, confirm: (leave) => leave() });

export const useUnsaved = () => useContext(UnsavedContext);

/**
 * Guards a browser's unsaved changes: switching table, page, database or
 * view, leaving the page or closing the tab first asks to discard them.
 */
export function UnsavedChanges({ children }: { children: ReactNode }) {
  const [dirty, setDirty] = useState(0);
  const [leave, setLeave] = useState<(() => void) | null>(null);
  const blocker = useBlocker(dirty > 0);
  useEffect(() => {
    if (!dirty) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);
  const latest = useRef(dirty);
  latest.current = dirty;
  const confirm = useCallback((fn: () => void) => (latest.current ? setLeave(() => fn) : fn()), []);
  const value = useMemo(() => ({ setDirty, confirm }), [confirm]);
  const asking = leave !== null || blocker.state === "blocked";
  const cancel = () => {
    setLeave(null);
    if (blocker.state === "blocked") blocker.reset();
  };

  return (
    <UnsavedContext.Provider value={value}>
      {children}
      <AlertDialog open={asking} onOpenChange={(o) => !o && cancel()}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Discard {dirty === 1 ? "1 unsaved change" : `${dirty} unsaved changes`}?</AlertDialogTitle>
            <AlertDialogDescription>Nothing was written to the database yet. Save first to keep them.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Keep editing</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                setDirty(0);
                if (blocker.state === "blocked") blocker.proceed();
                leave?.();
                setLeave(null);
              }}
            >
              Discard
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </UnsavedContext.Provider>
  );
}

export type ChangeCount = { n: number; label: string; tone: "edit" | "new" | "delete" };

const countTone = { edit: "bg-amber-500", new: "bg-emerald-500", delete: "bg-destructive" };

/** The footer's summary of pending changes, with Discard and Save (also Ctrl+S). */
export function ChangeBar({ counts, onDiscard, onSave }: { counts: ChangeCount[]; onDiscard: () => void; onSave: () => void }) {
  const total = counts.reduce((n, c) => n + c.n, 0);
  const save = useRef(onSave);
  save.current = onSave;
  useEffect(() => {
    if (!total) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "s" && (e.ctrlKey || e.metaKey)) {
        e.preventDefault();
        save.current();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [total]);
  if (!total) return null;
  return (
    <div className="ml-auto flex flex-wrap items-center gap-x-3 gap-y-1.5" role="status">
      <span className="flex flex-wrap gap-x-3 text-xs text-muted-foreground">
        {counts
          .filter((c) => c.n > 0)
          .map((c) => (
            <span key={c.tone} className="flex items-center gap-1.5 tabular-nums">
              <span className={cn("size-1.5 rounded-full", countTone[c.tone])} />
              {c.n} {c.label}
            </span>
          ))}
      </span>
      <Button variant="ghost" size="sm" onClick={onDiscard}>
        <Undo2 /> Discard
      </Button>
      <Button variant="destructive" size="sm" onClick={onSave}>
        <Save /> Save
        <kbd className="ml-1 hidden font-sans text-[0.7rem] opacity-60 sm:inline">Ctrl S</kbd>
      </Button>
    </div>
  );
}

/** Colors a statement's verb like the rows it touches: removed, changed, added. */
const verbTone: [RegExp, string][] = [
  [/^(DELETE|DEL|HDEL|SREM|ZREM|LREM|XDEL)\b|\.delete(One|Many)\(/, "text-destructive"],
  [/^(UPDATE|SET|HSET|LSET|ZADD XX|RENAME)\b|\.(updateOne|replaceOne)\(/, "text-amber-600 dark:text-amber-400"],
  [/^(INSERT|RPUSH|SADD|ZADD|XADD)\b|\.insertOne\(/, "text-emerald-600 dark:text-emerald-400"],
];

/**
 * Shows the statements a save will run, from the server, and runs them on
 * confirmation. run(false) previews; run(true) applies.
 */
export function ReviewDialog({
  open,
  onOpenChange,
  run,
  onSaved,
  onFailed,
  atomic = "They run in one transaction: if one fails, or a row changed since you loaded it, nothing is saved.",
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  run: (apply: boolean) => Promise<ChangeResult>;
  onSaved: (res: ChangeResult) => void;
  /** A save failed: some changes may have been saved anyway when they don't run as one transaction. */
  onFailed?: () => void;
  /** How the statements run together. */
  atomic?: string;
}) {
  const preview = useMutation({ meta: { error: false }, mutationFn: () => run(false) });
  const apply = useMutation({
    meta: { error: false },
    mutationFn: () => run(true),
    onSuccess: (res) => {
      onOpenChange(false);
      onSaved(res);
    },
    onError: () => onFailed?.(),
  });
  const { mutate: load, reset: resetPreview } = preview;
  const { reset: resetApply } = apply;
  useEffect(() => {
    if (!open) return;
    resetApply();
    load();
    return resetPreview;
  }, [open, load, resetPreview, resetApply]);
  const statements = preview.data?.statements ?? [];
  const error = preview.error ?? apply.error;

  return (
    <Dialog open={open} onOpenChange={(o) => !apply.isPending && onOpenChange(o)}>
      <DialogContent className="flex max-h-[85vh] flex-col gap-0 p-0 sm:max-w-3xl">
        <DialogHeader className="border-b p-4">
          <DialogTitle>Review changes</DialogTitle>
          <DialogDescription>
            {statements.length > 0 && `${statements.length === 1 ? "1 statement" : `${statements.length} statements`}. `}
            {atomic}
          </DialogDescription>
        </DialogHeader>
        <div className="min-h-0 flex-1 overflow-y-auto">
          {preview.isPending && <Loading className="py-8" />}
          {statements.length > 0 && (
            <ol className="divide-y font-mono text-xs" aria-label="Statements">
              {statements.map((s, i) => {
                const tone = verbTone.find(([re]) => re.test(s))?.[1];
                // The verb: SQL's first word, or a mongosh call up to its parenthesis.
                const [, verb, rest] = /^(db\..*?\.\w+(?=\()|\S+)([\s\S]*)$/.exec(s) ?? [, s, ""];
                return (
                  <li key={i} className="flex gap-3 px-4 py-2">
                    <span className="w-5 shrink-0 text-right text-muted-foreground tabular-nums select-none">{i + 1}</span>
                    <pre className="min-w-0 flex-1 wrap-break-word whitespace-pre-wrap">
                      <span className={cn("font-semibold", tone)}>{verb}</span>
                      {rest}
                    </pre>
                  </li>
                );
              })}
            </ol>
          )}
          {error && (
            <div className="p-4">
              <ConsoleError message={consoleError(error)} />
            </div>
          )}
        </div>
        <DialogFooter className="m-0">
          <Button variant="outline" disabled={apply.isPending} onClick={() => onOpenChange(false)}>
            Keep editing
          </Button>
          <Button variant="destructive" loading={apply.isPending} disabled={!preview.isSuccess} onClick={() => apply.mutate()}>
            <Save /> Save changes
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
