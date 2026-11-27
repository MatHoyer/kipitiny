import { Database, SquareTerminal } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useState, type CSSProperties } from "react";
import { Empty, ErrorText } from "@/components/common";
import { Loading } from "@kipit-labs/kipitidy/loading";
import { serviceLogos } from "@/components/service-icon";
import { Segmented, SegmentedItem } from "@kipit-labs/kipitidy/segmented";
import { api, isDatabase, type Service } from "../../api";
import { UnsavedChanges, useUnsaved } from "./changes";
import { MongoConsole, PgConsole, preloadSqlEditor, RedisConsole } from "./data-console";
import { DatabasePicker, NewDatabaseButton } from "./database-picker";
import { MongoBrowser } from "./mongo-browser";
import { PgBrowser } from "./pg-browser";
import { RedisBrowser } from "./redis-browser";

/** SQLite's light blue: its navy disappears on dark backgrounds. */
const sqliteColor = "#0F80CC";

/**
 * A database's data: browse (and edit) it, or run queries in a console.
 * An app's databases are the SQLite files in its volumes. Accents take the
 * database's brand hue (--db).
 */
export function DataTab({ svc }: { svc: Service }) {
  return (
    <UnsavedChanges>
      <Data svc={svc} />
    </UnsavedChanges>
  );
}

function Data({ svc }: { svc: Service }) {
  const { confirm } = useUnsaved();
  const [view, setView] = useState<"browse" | "console">("browse");
  // SQL kinds share the PostgreSQL browser and console; MongoDB has its own.
  const app = svc.kind === "app";
  const sql = app ? "sqlite" : svc.kind === "postgres" || svc.kind === "mysql" || svc.kind === "mariadb" ? svc.kind : null;
  useEffect(() => {
    if (sql) preloadSqlEditor();
  }, [sql]);
  // An instance may hold several databases (all but Redis); browse and console share the pick.
  const multi = svc.kind !== "redis";
  const databases = useQuery({
    queryKey: ["data", svc.id, "databases"],
    queryFn: () => api.pgDatabases(svc.id),
    enabled: multi && (app || isDatabase(svc.kind)),
  });
  const [picked, setPicked] = useState("");
  const dbs = databases.data ?? [];
  // An app has no main database: its first file is the default.
  const database = dbs.some((d) => d.name === picked) ? picked : (dbs.find((d) => d.main)?.name ?? (app ? (dbs[0]?.name ?? "") : ""));

  if (app) {
    // Its files are read through a helper: the app may be stopped.
    if (databases.isPending) return <Loading />;
    if (databases.error) return <ErrorText error={databases.error} />;
    if (dbs.length === 0)
      return (
        <Empty>
          No SQLite database in this app&apos;s volumes. One kept in the container&apos;s own files is lost on the next deploy: keep it in a volume.
        </Empty>
      );
  } else if (!isDatabase(svc.kind)) return null;
  else if (!svc.containers.some((c) => !c.retired && c.state === "running")) return <Empty>The database is stopped. Start it to see its data.</Empty>;
  const hue = { "--db": app ? sqliteColor : serviceLogos[svc.kind].color } as CSSProperties;

  return (
    <div className="space-y-5" style={hue}>
      <div className="flex flex-wrap items-center gap-2">
        <Segmented
          size="sm"
          value={view}
          onValueChange={(v) => confirm(() => setView(v as typeof view))}
          aria-label="View"
        >
          <SegmentedItem value="browse">
            <Database /> Browse
          </SegmentedItem>
          <SegmentedItem value="console">
            <SquareTerminal /> Console
          </SegmentedItem>
        </Segmented>
        {multi && (
          <div className="ml-auto flex items-center gap-1.5">
            <DatabasePicker databases={dbs} value={database} onChange={(d) => confirm(() => setPicked(d))} />
            {!app && <NewDatabaseButton serviceId={svc.id} mongo={svc.kind === "mongodb"} onCreated={(d) => confirm(() => setPicked(d))} />}
          </div>
        )}
      </div>
      {sql ? (
        view === "console" ? (
          <PgConsole key={database} serviceId={svc.id} database={database} dialect={sql} />
        ) : (
          <PgBrowser
            key={database}
            serviceId={svc.id}
            database={database}
            defaultSchema={sql === "postgres" ? "public" : sql === "sqlite" ? "main" : database}
            dialect={sql}
          />
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
