import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, RefreshCw } from "lucide-react";
import { useRef, useState } from "react";
import { ErrorText } from "@/components/common";
import { Loading } from "@kipit-labs/kipitidy/loading";
import { Button } from "@kipit-labs/kipitidy/button";
import { cn } from "@/lib/utils";
import { api } from "../../api";
import { BrowserShell, SidebarNav, SidebarHeader, SidebarItem } from "./browser-shell";
import { useUnsaved } from "./changes";
import { CollectionView } from "./mongo-collection-view";
import { NewCollectionDialog } from "./schema-dialogs";
import { WriteMode } from "./write-mode";

/** A MongoDB database's collections on the left, the picked one's documents as JSON. */
export function MongoBrowser({ serviceId, database }: { serviceId: string; database: string }) {
  const collections = useQuery({ queryKey: ["data", serviceId, "tables", database], queryFn: () => api.pgTables(serviceId, database) });
  const [picked, setPicked] = useState<string | null>(null);
  const [find, setFind] = useState("");
  const [sidebar, setSidebar] = useState(true);
  const navScroll = useRef(0);
  // Kept across collections: switching doesn't lock it again.
  const [write, setWrite] = useState(false);
  const [creating, setCreating] = useState(false);
  const { confirm } = useUnsaved();
  const queryClient = useQueryClient();
  const pick = (name: string) => confirm(() => setPicked(name));

  if (collections.isPending) return <Loading />;
  if (collections.error) return <ErrorText error={collections.error} />;
  const list = collections.data ?? [];
  const writeSwitch = (
    <WriteMode
      className="ml-auto"
      write={write}
      onChange={(w) => (w ? setWrite(true) : confirm(() => setWrite(false)))}
      description="You can then edit, add and delete documents, and create or drop collections. Nothing is written until you review the calls and confirm; only a restore from a backup undoes it."
    />
  );
  const newCollection = (
    <NewCollectionDialog
      open={creating}
      onOpenChange={setCreating}
      run={(name, apply) => api.createCollection(serviceId, database, name, apply)}
      onCreated={(name) => {
        queryClient.invalidateQueries({ queryKey: ["data", serviceId, "tables", database] });
        setPicked(name);
      }}
    />
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
            <Button variant="outline" size="icon-sm" title="Reload collections" onClick={() => collections.refetch()}>
              <RefreshCw className={cn(collections.isFetching && "animate-spin")} />
            </Button>
            {newCollection}
          </>
        }
      >
        <div className="flex flex-col items-center gap-3 p-10 text-center text-sm text-muted-foreground">
          <p>No collections in this database yet. They show up here once your app writes to them.</p>
          {write ? (
            <Button variant="outline" size="sm" onClick={() => setCreating(true)}>
              <Plus /> Collection
            </Button>
          ) : (
            <p>Or allow writes to create one.</p>
          )}
        </div>
      </BrowserShell>
    );
  const current = list.find((c) => c.name === picked) ?? list[0];
  const shown = list.filter((c) => c.name.toLowerCase().includes(find.trim().toLowerCase()));

  const nav = (
    <>
      <SidebarHeader
        title="Collections"
        search={find}
        onSearch={setFind}
        actions={
          <>
            {write && (
              <Button variant="ghost" size="icon-xs" title="New collection" onClick={() => confirm(() => setCreating(true))}>
                <Plus />
              </Button>
            )}
            <Button variant="ghost" size="icon-xs" title="Reload collections" onClick={() => collections.refetch()}>
              <RefreshCw className={cn(collections.isFetching && "animate-spin")} />
            </Button>
          </>
        }
      />
      {newCollection}
      <SidebarNav scroll={navScroll} aria-label="Collections" className="flex-1 space-y-px overflow-y-auto p-2">
        {shown.length === 0 && <p className="px-2 text-xs text-muted-foreground">No collection matches.</p>}
        {shown.map((c) => (
          <SidebarItem key={c.name} selected={c.name === current.name} onClick={() => pick(c.name)} title={c.kind}>
            <span className="min-w-0 flex-1 truncate">{c.name}</span>
            {c.kind !== "collection" && <span className="text-[0.65rem] opacity-70">{c.kind}</span>}
          </SidebarItem>
        ))}
      </SidebarNav>
    </>
  );

  return (
    <CollectionView
      key={current.name}
      serviceId={serviceId}
      database={database}
      collection={current}
      collections={list}
      onPick={pick}
      sidebar={nav}
      sidebarOpen={sidebar}
      onToggleSidebar={() => setSidebar(!sidebar)}
      write={write}
      writeSwitch={writeSwitch}
      onDropped={() => {
        setPicked(null);
        queryClient.invalidateQueries({ queryKey: ["data", serviceId, "tables", database] });
      }}
    />
  );
}
