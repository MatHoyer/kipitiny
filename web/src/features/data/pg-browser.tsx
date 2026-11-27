import { useQuery, useQueryClient } from "@tanstack/react-query";
import { FolderOpen, Plus, RefreshCw } from "lucide-react";
import { useRef, useState } from "react";
import { ErrorText } from "@/components/common";
import { Loading } from "@kipit-labs/kipitidy/loading";
import { Button } from "@kipit-labs/kipitidy/button";
import { cn } from "@/lib/utils";
import { api, type PgFilter, type PgRef } from "../../api";
import { BrowserShell, SidebarNav, SidebarHeader, SidebarItem } from "./browser-shell";
import { useUnsaved } from "./changes";
import { TableView } from "./pg-table-view";
import { NewTableDialog } from "./schema-dialogs";
import type { SqlDialect } from "./sql-editor";
import { tableKey } from "./table-key";
import { WriteMode } from "./write-mode";

/** defaultSchema's tables are listed without a heading: public, or MySQL's database. */
export function PgBrowser({ serviceId, database, defaultSchema, dialect }: { serviceId: string; database: string; defaultSchema: string; dialect: SqlDialect }) {
  const tables = useQuery({ queryKey: ["data", serviceId, "tables", database], queryFn: () => api.pgTables(serviceId, database) });
  const [picked, setPicked] = useState<string | null>(null);
  const [find, setFind] = useState("");
  const [sidebar, setSidebar] = useState(true);
  const navScroll = useRef(0);
  // Kept across tables: switching table doesn't lock it again.
  const [write, setWrite] = useState(false);
  const [creating, setCreating] = useState(false);
  // Where a followed foreign key leads: its table opens filtered on the value.
  const [jump, setJump] = useState<{ seq: number; filters: PgFilter[] } | null>(null);
  const { confirm } = useUnsaved();
  const queryClient = useQueryClient();
  const pick = (key: string) =>
    confirm(() => {
      setPicked(key);
      setJump(null);
    });
  const follow = (ref: PgRef, value: string) =>
    confirm(() => {
      setPicked(tableKey({ schema: ref.schema, name: ref.table }));
      setJump((j) => ({ seq: (j?.seq ?? 0) + 1, filters: [{ column: ref.column, op: "=", value }] }));
    });

  if (tables.isPending) return <Loading />;
  if (tables.error) return <ErrorText error={tables.error} />;
  const list = tables.data ?? [];
  const writeSwitch = (
    <WriteMode
      className="ml-auto"
      write={write}
      onChange={(w) => (w ? setWrite(true) : confirm(() => setWrite(false)))}
      description="You can then edit cells, add and delete rows, and create or drop tables. Nothing is written until you review the statements and confirm; only a restore from a backup undoes it."
    />
  );
  const newTable = (
    <NewTableDialog
      open={creating}
      onOpenChange={setCreating}
      dialect={dialect}
      defaultSchema={defaultSchema}
      run={(schema, name, columns, apply) => api.createTable(serviceId, database, schema, name, columns, apply)}
      onCreated={(schema, name) => {
        queryClient.invalidateQueries({ queryKey: ["data", serviceId, "tables", database] });
        setPicked(tableKey({ schema: dialect === "postgres" ? schema || "public" : defaultSchema, name }));
      }}
    />
  );
  const newTableButton = write && (
    <Button variant="ghost" size="icon-xs" title="New table" onClick={() => confirm(() => setCreating(true))}>
      <Plus />
    </Button>
  );
  if (list.length === 0)
    return (
      <BrowserShell
        write={write}
        sidebarOpen={false}
        sidebar={null}
        toolbar={
          <>
            {writeSwitch}
            <Button variant="outline" size="icon-sm" title="Reload tables" onClick={() => tables.refetch()}>
              <RefreshCw className={cn(tables.isFetching && "animate-spin")} />
            </Button>
            {newTable}
          </>
        }
      >
        <div className="flex flex-col items-center gap-3 p-10 text-center text-sm text-muted-foreground">
          <p>No tables in this database yet. Once your app runs its migrations, its tables show up here.</p>
          {write ? (
            <Button variant="outline" size="sm" onClick={() => setCreating(true)}>
              <Plus /> Table
            </Button>
          ) : (
            <p>Or allow writes to create one.</p>
          )}
        </div>
      </BrowserShell>
    );
  const current = list.find((t) => tableKey(t) === picked) ?? list[0];
  const shown = list.filter((t) => tableKey(t).toLowerCase().includes(find.trim().toLowerCase()));
  const schemas = [...new Set(shown.map((t) => t.schema))];

  const nav = (
    <>
      <SidebarHeader
        title="Tables"
        search={find}
        onSearch={setFind}
        actions={
          <>
            {newTableButton}
            <Button variant="ghost" size="icon-xs" title="Reload tables" onClick={() => tables.refetch()}>
              <RefreshCw className={cn(tables.isFetching && "animate-spin")} />
            </Button>
          </>
        }
      />
      {newTable}
      <SidebarNav scroll={navScroll} aria-label="Tables" className="flex-1 space-y-3 overflow-y-auto p-2">
        {shown.length === 0 && <p className="px-2 text-xs text-muted-foreground">No table matches.</p>}
        {schemas.map((schema) => {
          // A schema heads its tables, nested under it, unless it's the only (default) one.
          const heading = schemas.length > 1 || schema !== defaultSchema;
          return (
            <div key={schema} className="space-y-1">
              {heading && (
                <p className="flex items-center gap-1.5 px-2 text-xs font-semibold text-foreground" title="Schema">
                  <FolderOpen className="size-3.5 shrink-0 text-muted-foreground" />
                  <span className="min-w-0 truncate">{schema}</span>
                </p>
              )}
              <div className={cn("space-y-px", heading && "ml-3.5 border-l pl-1.5")}>
                {shown
                  .filter((t) => t.schema === schema)
                  .map((t) => (
                    <SidebarItem key={tableKey(t)} selected={tableKey(t) === tableKey(current)} onClick={() => pick(tableKey(t))} title={t.kind}>
                      <span className="min-w-0 flex-1 truncate">{t.name}</span>
                      {t.kind === "view" && <span className="text-[0.65rem] opacity-70">view</span>}
                      {t.kind === "materialized view" && <span className="text-[0.65rem] opacity-70">mview</span>}
                    </SidebarItem>
                  ))}
              </div>
            </div>
          );
        })}
      </SidebarNav>
    </>
  );

  return (
    <TableView
      // A new view on each jump, even to the same table (a self-reference).
      key={`${tableKey(current)}:${jump?.seq ?? 0}`}
      serviceId={serviceId}
      database={database}
      table={current}
      tables={list}
      onPick={pick}
      initialFilters={jump?.filters}
      onFollow={follow}
      sidebar={nav}
      sidebarOpen={sidebar}
      onToggleSidebar={() => setSidebar(!sidebar)}
      dialect={dialect}
      write={write}
      writeSwitch={writeSwitch}
      onDropped={() => {
        setPicked(null);
        queryClient.invalidateQueries({ queryKey: ["data", serviceId, "tables", database] });
      }}
    />
  );
}
