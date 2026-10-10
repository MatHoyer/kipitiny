import { Database, SquareTerminal } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useState, type CSSProperties } from "react";
import { Empty } from "@/components/common";
import { serviceLogos } from "@/components/service-icon";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { api, isDatabase, type Service } from "../../api";
import { MongoConsole, PgConsole, preloadSqlEditor, RedisConsole } from "./data-console";
import { DatabasePicker, NewDatabaseButton } from "./database-picker";
import { MongoBrowser } from "./mongo-browser";
import { PgBrowser } from "./pg-browser";
import { RedisBrowser } from "./redis-browser";

/** A database's data: browse it, or run queries in a console. Accents take the database's brand hue (--db). */
export function DataTab({ svc }: { svc: Service }) {
  const [view, setView] = useState<"browse" | "console">("browse");
  // SQL kinds share the PostgreSQL browser and console; MongoDB has its own.
  const sql = svc.kind === "postgres" || svc.kind === "mysql" || svc.kind === "mariadb" ? svc.kind : null;
  useEffect(() => {
    if (sql) preloadSqlEditor();
  }, [sql]);
  // An instance may hold several databases (all but Redis); browse and console share the pick.
  const multi = svc.kind !== "redis";
  const databases = useQuery({
    queryKey: ["data", svc.id, "databases"],
    queryFn: () => api.pgDatabases(svc.id),
    enabled: multi && isDatabase(svc.kind),
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
        {multi && (
          <div className="ml-auto flex items-center gap-1.5">
            <DatabasePicker databases={dbs} value={database} onChange={setPicked} />
            <NewDatabaseButton serviceId={svc.id} mongo={svc.kind === "mongodb"} onCreated={setPicked} />
          </div>
        )}
      </div>
      {sql ? (
        view === "console" ? (
          <PgConsole key={database} serviceId={svc.id} database={database} dialect={sql} />
        ) : (
          <PgBrowser key={database} serviceId={svc.id} database={database} defaultSchema={sql === "postgres" ? "public" : database} />
        )
      ) : svc.kind === "mongodb" ? (
        view === "console" ? (
          <MongoConsole key={database} serviceId={svc.id} database={database} />
        ) : (
          <MongoBrowser key={database} serviceId={svc.id} database={database} />
        )
      ) : view === "console" ? (
        <RedisConsole serviceId={svc.id} />
      ) : (
        <RedisBrowser serviceId={svc.id} />
      )}
    </div>
  );
}
