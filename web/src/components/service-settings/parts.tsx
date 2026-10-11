import { Clock, Zap } from "lucide-react";
import type { ReactNode } from "react";
import { Mono, Tag } from "@/components/common";
import { cn } from "@/lib/utils";

/** A card's title with when its changes take effect. */
export function Applies({ title, when }: { title: ReactNode; when: "now" | "deploy" }) {
  return (
    <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
      {title}
      <Tag
        className={cn(
          "inline-flex items-center gap-1 font-normal",
          when === "now" && "bg-emerald-500/10 text-emerald-700 dark:text-emerald-400",
        )}
      >
        {when === "now" ? <Zap className="size-3" /> : <Clock className="size-3" />}
        {when === "now" ? "Applies now" : "On next deploy"}
      </Tag>
    </span>
  );
}

/** Says why a git project's service settings are read-only. */
export function GitManagedNote({ path }: { path: string }) {
  return (
    <p className="rounded-md border border-dashed px-3 py-2 text-sm text-muted-foreground">
      This project follows <Mono>{path}</Mono> in a git repository: change the service there. Deploying another image tag from CI still
      works until the file changes this service.
    </p>
  );
}

/** Cards the compose file owns: in a git project, grouped under the note and read-only. */
export function GitOwned({ path, children }: { path?: string; children: ReactNode }) {
  if (!path) return children;
  return (
    <div className="flex flex-col gap-4 rounded-2xl border border-dashed p-3">
      <p className="text-sm text-muted-foreground">
        Set in <Mono>{path}</Mono>: change these in the repository. Deploying another image tag from CI still works until the file changes
        this service.
      </p>
      <fieldset disabled className="contents">
        {children}
      </fieldset>
    </div>
  );
}
