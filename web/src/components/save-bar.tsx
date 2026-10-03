import { CircleDot } from "lucide-react";
import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

const HostContext = createContext<HTMLElement | null>(null);

/**
 * Where save bars float: stuck to the bottom of the scrolling content, so they
 * stay centered on it and stack when several forms are dirty.
 */
export function SaveBarHost({ children }: { children: ReactNode }) {
  const [host, setHost] = useState<HTMLElement | null>(null);
  return (
    <HostContext.Provider value={host}>
      {children}
      <div className="pointer-events-none sticky bottom-0 z-30 mt-auto">
        <div ref={setHost} className="flex flex-col items-center gap-2 px-4 pb-4 empty:hidden" />
      </div>
    </HostContext.Provider>
  );
}

/**
 * Slides up while its form has unsaved changes. Its buttons sit outside the
 * form's DOM, so they reach it by id (native validation still runs).
 */
export function SaveBar({
  form,
  dirty,
  saving,
  onReset,
  label = "Save",
}: {
  form: string;
  dirty: boolean;
  saving: boolean;
  onReset: () => void;
  label?: string;
}) {
  const host = useContext(HostContext);
  // Kept mounted after the form gets clean, until the exit animation ends.
  const [shown, setShown] = useState(dirty);
  useEffect(() => {
    if (dirty) setShown(true);
  }, [dirty]);
  const visible = dirty || saving;

  if (!host || !shown) return null;
  return createPortal(
    <div
      role="region"
      aria-label="Unsaved changes"
      onAnimationEnd={() => !visible && setShown(false)}
      className={cn(
        "pointer-events-auto flex w-full max-w-xl items-center gap-3 rounded-xl border bg-popover/95 py-2 pr-2 pl-4 text-sm text-popover-foreground shadow-lg backdrop-blur duration-200",
        visible
          ? "animate-in fade-in-0 slide-in-from-bottom-4"
          : "pointer-events-none animate-out fill-mode-forwards fade-out-0 slide-out-to-bottom-4",
      )}
    >
      <CircleDot className="size-4 shrink-0 text-amber-500" />
      <span className="flex-1 truncate">Unsaved changes</span>
      <Button type="button" variant="ghost" size="sm" disabled={saving} onClick={onReset}>
        Reset
      </Button>
      <Button type="submit" form={form} size="sm" loading={saving}>
        {label}
      </Button>
    </div>,
    host,
  );
}
