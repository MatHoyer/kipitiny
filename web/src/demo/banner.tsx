import { RotateCcw } from "lucide-react";
import { Button } from "@/components/ui/button";

/** Says it's a demo, and starts it over. Kept free of the backend so normal builds can drop it. */
export function DemoBanner() {
  const reset = () => {
    try {
      localStorage.removeItem("kipitiny-demo");
    } catch {
      // Nothing stored.
    }
    location.reload();
  };
  return (
    <div className="flex items-center justify-center gap-3 border-b bg-amber-100 px-4 py-1.5 text-xs text-amber-950 dark:bg-amber-950 dark:text-amber-100">
      <span>Demo: everything runs and is saved in your browser, nothing reaches a server.</span>
      <Button variant="ghost" size="sm" className="h-6 px-2 text-xs" onClick={reset}>
        <RotateCcw data-icon="inline-start" />
        Reset
      </Button>
    </div>
  );
}
