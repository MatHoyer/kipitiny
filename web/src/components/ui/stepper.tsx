import { cn } from "cn";
import type { CSSProperties, ReactNode } from "react";

export type Step = { title: ReactNode; hint?: ReactNode };

type State = "done" | "active" | "failed" | "todo";

/**
 * A stepper. The current step's number is circled by an orbiting arc (faster while `busy`), done steps pop
 * into a filled disc whose check draws itself, and the rail between them fills as you go. `failed` turns the
 * current step into a cross. Horizontal shows titles under the steps; vertical shows each title and hint beside
 * it, to describe a process as it runs. `accent` tints it (e.g. a brand color); it defaults to the primary color.
 */
export function Stepper({
  steps,
  current,
  busy,
  failed,
  accent,
  orientation = "horizontal",
  onStepClick,
  className,
}: {
  steps: Step[];
  /** Index of the active step; `steps.length` marks them all done. */
  current: number;
  busy?: boolean;
  failed?: boolean;
  accent?: string;
  orientation?: "horizontal" | "vertical";
  /** Lets steps be clicked: done ones when horizontal (to go back to them), any when vertical. */
  onStepClick?: (index: number) => void;
  className?: string;
}) {
  const style = accent ? ({ "--step": accent, "--step-fg": "white" } as CSSProperties) : undefined;
  const stateOf = (i: number): State => (i < current ? "done" : i > current ? "todo" : failed ? "failed" : "active");

  if (orientation === "vertical") {
    return (
      <ol className={cn("stepper grid", className)} style={style}>
        {steps.map((s, i) => {
          const state = stateOf(i);
          return (
            <li key={i} aria-current={state === "active" ? "step" : undefined} className="relative flex gap-3 pb-3.5 last:pb-0">
              {i < steps.length - 1 && <Rail filled={i < current} vertical />}
              {onStepClick ? (
                <button
                  type="button"
                  title={state === "done" ? "Mark as not done" : "Mark as done"}
                  onClick={() => onStepClick(i)}
                  className="h-fit rounded-full outline-none transition-transform hover:scale-110 focus-visible:ring-2 focus-visible:ring-ring/50"
                >
                  <StepNode n={i + 1} state={state} busy={busy && state === "active"} />
                </button>
              ) : (
                <StepNode n={i + 1} state={state} busy={busy && state === "active"} />
              )}
              <div className="min-w-0 flex-1 pt-1.5">
                <p className={cn("text-sm font-medium transition-colors", state === "todo" ? "text-muted-foreground" : "text-foreground")}>
                  {s.title}
                </p>
                {s.hint && <div className="text-xs text-muted-foreground">{s.hint}</div>}
              </div>
            </li>
          );
        })}
      </ol>
    );
  }

  return (
    <ol className={cn("stepper flex items-start", className)} style={style}>
      {steps.map((s, i) => {
        const state = stateOf(i);
        const clickable = state === "done" && onStepClick;
        return (
          <li key={i} className="relative flex flex-1 flex-col items-center gap-1.5 text-center">
            {i > 0 && <Rail filled={i <= current} />}
            <button
              type="button"
              disabled={!clickable}
              onClick={() => clickable && onStepClick(i)}
              aria-current={state === "active" ? "step" : undefined}
              className="group flex flex-col items-center gap-1.5 rounded-md outline-none focus-visible:ring-2 focus-visible:ring-ring/50 disabled:cursor-default"
            >
              <StepNode n={i + 1} state={state} busy={busy && state === "active"} />
              <span
                className={cn(
                  "text-xs font-medium transition-colors",
                  state === "todo" ? "text-muted-foreground" : "text-foreground",
                  clickable && "group-hover:underline",
                )}
              >
                {s.title}
              </span>
            </button>
            {s.hint && <span className="-mt-1 text-[0.7rem] text-muted-foreground">{s.hint}</span>}
          </li>
        );
      })}
    </ol>
  );
}

/** The line between two steps, filling toward the next one. */
function Rail({ filled, vertical }: { filled: boolean; vertical?: boolean }) {
  return (
    <span
      aria-hidden
      className={cn(
        "absolute overflow-hidden rounded-full bg-border",
        vertical ? "top-9.5 bottom-1.5 left-4 w-0.5 -translate-x-1/2" : "top-[0.9375rem] right-[calc(50%+1.375rem)] left-[calc(-50%+1.375rem)] h-0.5",
      )}
    >
      <span
        className={cn("block size-full rounded-full bg-(--step) transition-transform duration-500 ease-out", vertical ? "origin-top" : "origin-left")}
        style={{ transform: `${vertical ? "scaleY" : "scaleX"}(${filled ? 1 : 0})` }}
      />
    </span>
  );
}

function StepNode({ n, state, busy }: { n: number; state: State; busy?: boolean }) {
  return (
    <span className="relative flex size-8 shrink-0 items-center justify-center">
      {state === "active" && <span aria-hidden className="stepper-halo absolute inset-0 rounded-full bg-(--step)" />}
      <svg aria-hidden viewBox="0 0 32 32" className="absolute inset-0 size-full">
        <circle
          cx="16"
          cy="16"
          r="14"
          fill="none"
          strokeWidth="1.5"
          className={cn("transition-[stroke]", state === "todo" ? "stroke-border" : "stroke-(--step)/25")}
          strokeDasharray={state === "todo" ? "2 3.5" : undefined}
        />
        {state === "active" && (
          <g className={cn("stepper-orbit", busy && "stepper-orbit-busy")}>
            <circle
              cx="16"
              cy="16"
              r="14"
              fill="none"
              strokeWidth="2"
              strokeLinecap="round"
              pathLength={1}
              strokeDasharray={busy ? "0.55 0.45" : "0.22 0.78"}
              className="stroke-(--step) transition-[stroke-dasharray] duration-500"
            />
            <circle cx="30" cy="16" r="2" className="fill-(--step)" />
          </g>
        )}
        {state === "done" && (
          <g className="stepper-pop">
            <circle cx="16" cy="16" r="15" className="fill-(--step)" />
            <path
              d="M10.5 16.5l3.5 3.5 7.5-8"
              fill="none"
              strokeWidth="2.25"
              strokeLinecap="round"
              strokeLinejoin="round"
              pathLength={1}
              className="stepper-draw stroke-(--step-fg)"
            />
          </g>
        )}
        {state === "failed" && (
          <g className="stepper-shake">
            <circle cx="16" cy="16" r="15" className="fill-destructive" />
            <path d="M11.5 11.5l9 9m0-9l-9 9" fill="none" strokeWidth="2.25" strokeLinecap="round" pathLength={1} className="stepper-draw stroke-white" />
          </g>
        )}
      </svg>
      {(state === "active" || state === "todo") && (
        <span
          key={state}
          className={cn(
            "relative font-mono text-xs tabular-nums",
            state === "active" ? "stepper-bump font-semibold text-(--step)" : "text-muted-foreground",
          )}
        >
          {n}
        </span>
      )}
      <span className="sr-only">{state === "done" ? "(done)" : state === "active" ? "(current)" : state === "failed" ? "(failed)" : ""}</span>
    </span>
  );
}
