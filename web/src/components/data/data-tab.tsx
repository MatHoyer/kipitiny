import { useState } from "react";
import { Empty } from "@/components/common";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import type { Service } from "../../api";
import { isDatabase } from "../../api";
import { DataConsole } from "./data-console";
import { PgBrowser } from "./pg-browser";
import { RedisBrowser } from "./redis-browser";

/** A database's data: browse it, or run queries in a console. */
export function DataTab({ svc }: { svc: Service }) {
  const [view, setView] = useState<"browse" | "console">("browse");
  if (!isDatabase(svc.kind)) return null;
  if (!svc.containers.some((c) => !c.retired && c.state === "running")) return <Empty>The database is not running.</Empty>;

  return (
    <div className="space-y-4">
      <ToggleGroup
        type="single"
        size="sm"
        variant="outline"
        spacing={0}
        value={view}
        onValueChange={(v) => v && setView(v as typeof view)}
        aria-label="View"
      >
        <ToggleGroupItem value="browse" className="aria-checked:bg-muted">
          Browse
        </ToggleGroupItem>
        <ToggleGroupItem value="console" className="aria-checked:bg-muted">
          Console
        </ToggleGroupItem>
      </ToggleGroup>
      {view === "console" ? (
        <DataConsole serviceId={svc.id} kind={svc.kind} />
      ) : svc.kind === "postgres" ? (
        <PgBrowser serviceId={svc.id} />
      ) : (
        <RedisBrowser serviceId={svc.id} />
      )}
    </div>
  );
}
