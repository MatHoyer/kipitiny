import { useEffect, useState } from "react";
import { useUnsaved, type ChangeCount } from "./changes";

/** Edits waiting for Save, each browser keyed its own way. */
type Staged = { edits: Record<string, unknown>; deletes: Record<string, unknown>; inserts: unknown[] };

/**
 * A browser view's pending changes: counted for the unsaved-changes guard,
 * dropped once writes are turned off (when agreed to). `extra` counts what
 * isn't an edit, delete or insert (a whole key to delete).
 */
export function usePending<P extends Staged>(nothing: P, writeAllowed: boolean, extra: (p: P) => number = () => 0) {
  const { setDirty } = useUnsaved();
  const [pending, setPending] = useState<P>(nothing);
  const edited = Object.keys(pending.edits).filter((k) => !pending.deletes[k]).length;
  const deleted = Object.keys(pending.deletes).length;
  const dirty = edited + deleted + pending.inserts.length + extra(pending);
  useEffect(() => setDirty(dirty), [dirty, setDirty]);
  useEffect(() => () => setDirty(0), [setDirty]);
  // Turning writes off (once agreed to) drops what was pending.
  useEffect(() => {
    if (!writeAllowed) setPending(nothing);
  }, [writeAllowed, nothing]);
  const counts: ChangeCount[] = [
    { n: edited, label: "edited", tone: "edit" },
    { n: pending.inserts.length, label: "new", tone: "new" },
    { n: deleted, label: "to delete", tone: "delete" },
  ];
  return { pending, setPending, counts };
}
