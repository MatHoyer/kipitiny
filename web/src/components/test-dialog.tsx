import { useMutation } from "@tanstack/react-query";
import { PlugZap, RotateCw } from "lucide-react";
import { useState, type ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { Stepper } from "@/components/ui/stepper";

/** A step a call may report (see the API's ?progress), said for the user. */
export type PlannedStep = {
  id: string;
  title: ReactNode;
  hint: ReactNode;
  /** Why it's done without being reported, e.g. "Already downloaded". */
  skipped?: ReactNode;
};

/**
 * The steps of a call as the manager reports them: the last one reported is current, steps it skipped are
 * marked done with why. Before it runs, the list says what will happen.
 */
export function StepsProgress({
  plan,
  seen,
  running,
  done,
  error,
  accent,
}: {
  plan: PlannedStep[];
  seen: string[];
  running: boolean;
  done: boolean;
  error: Error | null;
  accent?: string;
}) {
  const last = seen.at(-1);
  // Nothing reported yet: it's at the first step. An error before any step (a malformed input) marks none.
  const current = done ? plan.length : last ? plan.findIndex((s) => s.id === last) : running ? 0 : -1;
  const steps = plan.map((s, i) => ({ title: s.title, hint: i < current && !seen.includes(s.id) ? (s.skipped ?? s.hint) : s.hint }));
  return (
    <>
      <Stepper orientation="vertical" steps={steps} current={current} busy={running} failed={!!error && !!last} accent={accent} />
      {error && <p className="text-sm text-destructive animate-in fade-in">{error.message}</p>}
    </>
  );
}

/** Tests a saved integration in a dialog that runs as it opens and shows each step. */
export function TestDialog({
  name,
  icon,
  description,
  accent,
  plan,
  run,
}: {
  name: string;
  icon?: ReactNode;
  description: ReactNode;
  accent?: string;
  /** The steps to show, given those reported so far (a test may take a longer path). */
  plan: PlannedStep[] | ((seen: string[]) => PlannedStep[]);
  run: (onStep: (step: string) => void) => Promise<unknown>;
}) {
  const [open, setOpen] = useState(false);
  const [seen, setSeen] = useState<string[]>([]);
  const test = useMutation({
    // The steps show where it failed instead.
    meta: { error: false },
    mutationFn: () => {
      setSeen([]);
      return run((s) => setSeen((prev) => [...prev, s]));
    },
  });
  const onOpenChange = (next: boolean) => {
    setOpen(next);
    if (next) test.mutate();
    else {
      test.reset();
      setSeen([]);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button variant="ghost" size="icon-sm" title="Test connection" aria-label="Test connection">
          <PlugZap />
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 [&>svg]:size-5">
            {icon}
            Test {name}
          </DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <StepsProgress
          plan={typeof plan === "function" ? plan(seen) : plan}
          seen={seen}
          running={test.isPending}
          done={test.isSuccess}
          error={test.error}
          accent={accent}
        />
        {test.isSuccess && <p className="text-sm font-medium animate-in fade-in">{name} works.</p>}
        <DialogFooter>
          <Button variant="outline" loading={test.isPending} onClick={() => test.mutate()}>
            <RotateCw data-icon="inline-start" />
            Test again
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
