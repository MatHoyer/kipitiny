import { Database, SquareTerminal } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useState, type CSSProperties } from "react";
import { Empty } from "@/components/common";
import { serviceLogos } from "@/components/service-icon";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { api, isDatabase, type Service } from "../../api";
import { PgConsole, preloadSqlEditor, RedisConsole } from "./data-console";
import { DatabasePicker, NewDatabaseButton } from "./database-picker";
import { PgBrowser } from "./pg-browser";
import { RedisBrowser } from "./redis-browser";

/** A database's data: browse it, or run queries in a console. Accents take the database's brand hue (--db). */
export function DataTab({ svc }: { svc: Service }) {
  const [view, setView] = useState<"browse" | "console">("browse");
  useEffect(() => {
    if (svc.kind === "postgres") preloadSqlEditor();
  }, [svc.kind]);
  // A postgres instance may hold several databases; browse and console share the pick.
  const databases = useQuery({
    queryKey: ["data", svc.id, "databases"],
    queryFn: () => api.pgDatabases(svc.id),
    enabled: svc.kind === "postgres",
  });
  const [picked, setPicked] = useState("");
  const dbs = databases.data ?? [];
  const database = dbs.some((d) => d.name === picked) ? picked : (dbs.find((d) => d.main)?.name ?? "");

  if (!isDatabase(svc.kind)) return null;
  if (!svc.containers.some((c) => !c.retired && c.state === "running"))
    return <Empty>The database is stopped. Start it to see its data.</Empty>;
  const hue = { "--db": serviceLogos[svc.kind].color } as CSSProperties;

  return (
    <div className="space-y-5" style={hue}>
      <div className="flex flex-wrap items-center gap-2">
        <ToggleGroup
          type="single"
          size="sm"
          variant="outline"
          spacing={0}
          value={view}
          onValueChange={(v) => v && setView(v as typeof view)}
          aria-label="View"
        >
          <ToggleGroupItem value="browse" className="text-muted-foreground data-[state=on]:text-foreground">
            <Database /> Browse
          </ToggleGroupItem>
          <ToggleGroupItem value="console" className="text-muted-foreground data-[state=on]:text-foreground">
            <SquareTerminal /> Console
          </ToggleGroupItem>
        </ToggleGroup>
        {svc.kind === "postgres" && (
          <div className="ml-auto flex items-center gap-1.5">
            <DatabasePicker databases={dbs} value={database} onChange={setPicked} />
            <NewDatabaseButton serviceId={svc.id} onCreated={setPicked} />
          </div>
        )}
      </div>
      {svc.kind === "postgres" ? (
        view === "console" ? (
          <PgConsole key={database} serviceId={svc.id} database={database} />
        ) : (
          <PgBrowser key={database} serviceId={svc.id} database={database} />
        )
      ) : view === "console" ? (
        <RedisConsole serviceId={svc.id} />
      ) : (
        <RedisBrowser serviceId={svc.id} />
      )}
    </div>
  );
}
