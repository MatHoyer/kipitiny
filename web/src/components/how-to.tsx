import { ChevronDown, CircleHelp, ExternalLink } from "lucide-react";
import { cn } from "cn";
import { useState, type ReactNode } from "react";
import { CopyField } from "@/components/common";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Stepper } from "@/components/ui/stepper";

export type HowToStep = {
  title: ReactNode;
  text: ReactNode;
  /** Where to do it, opened in a new tab. */
  link?: { href: string; label: string };
  /** Fields that shape `copy`, e.g. a vault name. */
  extra?: ReactNode;
  /** A command or snippet to copy; copying it marks the step done. */
  copy?: string;
};

/**
 * Setup steps folded behind a link ("How do I get a token?"). They show as a vertical stepper: copying a step's
 * snippet or clicking its number marks it done, so the orbit follows along.
 */
export function HowTo({ label, steps, accent, className }: { label: ReactNode; steps: HowToStep[]; accent?: string; className?: string }) {
  const [open, setOpen] = useState(false);
  const [done, setDone] = useState(0);
  const reach = (i: number) => setDone((d) => Math.max(d, i + 1));

  return (
    <Collapsible open={open} onOpenChange={setOpen} className={className}>
      <CollapsibleTrigger asChild>
        <Button type="button" variant="link" size="sm" className="h-auto px-0 text-muted-foreground">
          <CircleHelp data-icon="inline-start" />
          {label}
          <ChevronDown data-icon="inline-end" className={cn("transition-transform", open && "rotate-180")} />
        </Button>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <Stepper
          orientation="vertical"
          accent={accent}
          current={done}
          onStepClick={(i) => setDone(i < done ? i : i + 1)}
          className="pt-3"
          steps={steps.map((s, i) => ({
            title: s.title,
            hint: (
              <div className="grid gap-2 pt-0.5">
                <p>{s.text}</p>
                {s.link && (
                  <a
                    href={s.link.href}
                    target="_blank"
                    rel="noreferrer"
                    onClick={() => reach(i)}
                    className="inline-flex w-fit items-center gap-1 font-medium text-foreground underline-offset-4 hover:underline"
                  >
                    {s.link.label}
                    <ExternalLink className="size-3" />
                  </a>
                )}
                {s.extra}
                {s.copy && <CopyField value={s.copy} onCopy={() => reach(i)} />}
              </div>
            ),
          }))}
        />
      </CollapsibleContent>
    </Collapsible>
  );
}
