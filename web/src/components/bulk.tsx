import { useMutation, useQueryClient } from "@tanstack/react-query";
import { X } from "lucide-react";
import { useState, type ReactNode } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { friendlyError } from "@/lib/errors";

type Item = { id: string; name: string };

/** Which items are checked; only the ones still listed count, so a filter or a deletion drops the rest. */
export function useSelection<T extends Item>(listed: T[]) {
  const [ids, setIds] = useState<ReadonlySet<string>>(new Set());
  const chosen = listed.filter((i) => ids.has(i.id));
  const toggle = (id: string, on: boolean) =>
    setIds((s) => {
      const next = new Set(s);
      if (on) next.add(id);
      else next.delete(id);
      return next;
    });
  return {
    chosen,
    has: (id: string) => ids.has(id),
    toggle,
    /** Header checkbox state: all, some or none of the listed items. */
    all: chosen.length > 0 && chosen.length === listed.length ? true : chosen.length > 0 ? ("indeterminate" as const) : false,
    setAll: (on: boolean) => setIds(new Set(on ? listed.map((i) => i.id) : [])),
    clear: () => setIds(new Set()),
  };
}

/**
 * Runs one request per item through the endpoints the single actions use, so each keeps its own permission
 * check; reports how many failed and why.
 */
export function useBulk<T extends Item>(onDone?: () => void) {
  const qc = useQueryClient();
  return useMutation({
    meta: { error: false },
    mutationFn: async ({ items, run }: { label: string; items: T[]; run: (i: T) => Promise<unknown> }) =>
      (await Promise.allSettled(items.map(run))).flatMap((r, n) => (r.status === "rejected" ? [`${items[n].name}: ${friendlyError(r.reason)}`] : [])),
    onSuccess: (failed, { label, items }) => {
      // Lists, pages and the map all show what changed.
      qc.invalidateQueries();
      if (!failed.length) toast.success(`${label}: ${items.length} done`);
      else
        toast.error(`${label}: ${failed.length} of ${items.length} failed`, {
          description: failed.join("\n"),
          duration: 12000,
        });
      onDone?.();
    },
  });
}

/** Floats under the list while something is checked. */
export function BulkBar({ count, noun, onClear, children }: { count: number; noun: string; onClear: () => void; children: ReactNode }) {
  if (!count) return null;
  return (
    <div className="sticky bottom-4 z-10 mx-auto flex w-fit max-w-full flex-wrap items-center gap-2 rounded-xl border bg-popover p-2 pl-4 shadow-lg">
      <span className="text-sm whitespace-nowrap">
        {count} {noun}
        {count === 1 ? "" : "s"} selected
      </span>
      {children}
      <Button size="icon-sm" variant="ghost" onClick={onClear} aria-label="Clear selection">
        <X />
      </Button>
    </div>
  );
}
