import { Moon } from "lucide-react";
import { stateColors } from "@/components/common";
import { ServiceIcon } from "@/components/service-icon";
import { needsAttention } from "@/components/topology";
import { liveState } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { Service } from "../api";

/** One icon per service with its state, then how the project is doing: needs attention, asleep or all running. */
export function ServiceStates({ services }: { services: Service[] }) {
  if (services.length === 0) return null;
  const states = services.map(liveState);
  const bad = services.filter(needsAttention).length;
  // A project sleeps whole: one service asleep means all of them are.
  const asleep = services.some((s) => s.sleeping);
  return (
    <div className="flex flex-wrap items-center gap-1.5" aria-label="Service states">
      {services.map((s, i) => (
        <span
          key={s.id}
          role="img"
          aria-label={`${s.name}: ${states[i]}`}
          title={`${s.name}: ${states[i]}`}
          className="relative flex size-6 items-center justify-center rounded-md bg-muted"
        >
          <ServiceIcon service={s} className="size-3.5 text-muted-foreground" />
          <span
            className={cn("absolute -right-0.5 -bottom-0.5 size-2 rounded-full ring-2 ring-card", needsAttention(s) ? "bg-red-500" : (stateColors[states[i]] ?? "bg-muted-foreground/40"))}
          />
        </span>
      ))}
      <span className="ml-1 inline-flex items-center gap-1 text-xs text-muted-foreground">
        {bad ? (
          `${bad} need${bad === 1 ? "s" : ""} attention`
        ) : asleep ? (
          <>
            <Moon className="size-3 text-blue-500" />
            asleep
          </>
        ) : states.every((s) => s === "running") ? (
          "all running"
        ) : (
          ""
        )}
      </span>
    </div>
  );
}
