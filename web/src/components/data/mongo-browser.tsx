import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { ArrowDownUp, ChevronLeft, ChevronRight, ChevronsLeft, ListFilter, RefreshCw } from "lucide-react";
import { useState, type ReactNode } from "react";
import { CopyButton, Empty, ErrorText, Loading } from "@/components/common";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { cn } from "@/lib/utils";
import { api, type PgTable } from "../../api";
import { BrowserShell, SidebarHeader, SidebarItem, SidebarToggle } from "./browser-shell";

const PAGE_SIZES = [10, 25, 50, 100];

/** A MongoDB database's collections on the left, the picked one's documents as JSON. */
export function MongoBrowser({ serviceId, database }: { serviceId: string; database: string }) {
  const collections = useQuery({ queryKey: ["data", serviceId, "tables", database], queryFn: () => api.pgTables(serviceId, database) });
  const [picked, setPicked] = useState<string | null>(null);
  const [find, setFind] = useState("");
  const [sidebar, setSidebar] = useState(true);

  if (collections.isPending) return <Loading />;
  if (collections.error) return <ErrorText error={collections.error} />;
  const list = collections.data ?? [];
  if (list.length === 0) return <Empty>No collections in this database yet. They show up here once your app writes to them.</Empty>;
  const current = list.find((c) => c.name === picked) ?? list[0];
  const shown = list.filter((c) => c.name.toLowerCase().includes(find.trim().toLowerCase()));

  const nav = (
    <>
      <SidebarHeader
        title="Collections"
        search={find}
        onSearch={setFind}
        actions={
          <Button variant="ghost" size="icon-xs" title="Reload collections" onClick={() => collections.refetch()}>
            <RefreshCw className={cn(collections.isFetching && "animate-spin")} />
          </Button>
        }
      />
      <nav aria-label="Collections" className="flex-1 space-y-px overflow-y-auto p-2">
        {shown.length === 0 && <p className="px-2 text-xs text-muted-foreground">No collection matches.</p>}
        {shown.map((c) => (
          <SidebarItem key={c.name} selected={c.name === current.name} onClick={() => setPicked(c.name)} title={c.kind}>
            <span className="min-w-0 flex-1 truncate">{c.name}</span>
            {c.kind !== "collection" && <span className="text-[0.65rem] opacity-70">{c.kind}</span>}
          </SidebarItem>
        ))}
      </nav>
    </>
  );

  return (
    <CollectionView
      key={current.name}
      serviceId={serviceId}
      database={database}
      collection={current}
      collections={list}
      onPick={setPicked}
      sidebar={nav}
      sidebarOpen={sidebar}
      onToggleSidebar={() => setSidebar(!sidebar)}
    />
  );
}

function CollectionView({
  serviceId,
  database,
  collection,
  collections,
  onPick,
  sidebar,
  sidebarOpen,
  onToggleSidebar,
}: {
  serviceId: string;
  database: string;
  collection: PgTable;
  collections: PgTable[];
  onPick: (name: string) => void;
  sidebar: ReactNode;
  sidebarOpen: boolean;
  onToggleSidebar: () => void;
}) {
  const [size, setSize] = useState(25);
  const [page, setPage] = useState(0);
  // Applied on Enter: half-typed JSON never reaches the server.
  const [filter, setFilter] = useState("");
  const [sort, setSort] = useState("");
  const q = { filter, sort, limit: size, skip: page * size };
  const docs = useQuery({
    queryKey: ["data", serviceId, "documents", database, collection.name, q],
    queryFn: () => api.mongoDocuments(serviceId, database, collection.name, q),
    placeholderData: keepPreviousData,
  });
  const data = docs.data;
  const pages = !filter && collection.rowEstimate > 0 ? Math.max(1, Math.ceil(collection.rowEstimate / size)) : null;

  return (
    <BrowserShell
      sidebar={sidebar}
      sidebarOpen={sidebarOpen}
      toolbar={
        <>
          <SidebarToggle open={sidebarOpen} onToggle={onToggleSidebar} />
          <Select value={collection.name} onValueChange={onPick}>
            <SelectTrigger size="sm" aria-label="Collection" className="font-mono text-xs md:hidden">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {collections.map((c) => (
                <SelectItem key={c.name} value={c.name} className="font-mono text-xs">
                  {c.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <JsonField
            icon={<ListFilter />}
            label="Filter"
            placeholder='{ "status": "active" }'
            value={filter}
            onApply={(v) => {
              setPage(0);
              setFilter(v);
            }}
            className="min-w-48 flex-1"
          />
          <JsonField
            icon={<ArrowDownUp />}
            label="Sort"
            placeholder='{ "_id": -1 }'
            value={sort}
            onApply={(v) => {
              setPage(0);
              setSort(v);
            }}
            className="w-40 sm:w-48"
          />
          <Button variant="outline" size="icon-sm" title="Reload documents" onClick={() => docs.refetch()}>
            <RefreshCw className={cn(docs.isFetching && "animate-spin")} />
          </Button>
        </>
      }
      footer={
        <>
          <div className="flex h-8 items-stretch overflow-hidden rounded-md border">
            <Button variant="ghost" size="icon-sm" className="h-auto rounded-none" title="First page" disabled={page === 0} onClick={() => setPage(0)}>
              <ChevronsLeft />
            </Button>
            <Button variant="ghost" size="icon-sm" className="h-auto rounded-none border-l" title="Previous page" disabled={page === 0} onClick={() => setPage(page - 1)}>
              <ChevronLeft />
            </Button>
            <span className="flex items-center border-l px-3 text-sm whitespace-nowrap tabular-nums">
              {page + 1}
              {pages && <span className="ml-1 text-muted-foreground">of {Intl.NumberFormat().format(Math.max(pages, page + 1))}</span>}
            </span>
            <Select
              value={String(size)}
              onValueChange={(v) => {
                setPage(0);
                setSize(Number(v));
              }}
            >
              <SelectTrigger size="sm" aria-label="Documents per page" className="h-auto! rounded-none border-0 border-l shadow-none">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {PAGE_SIZES.map((n) => (
                  <SelectItem key={n} value={String(n)}>
                    {n} documents per page
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Button variant="ghost" size="icon-sm" className="h-auto rounded-none border-l" title="Next page" disabled={!data?.hasMore} onClick={() => setPage(page + 1)}>
              <ChevronRight />
            </Button>
          </div>
          <ErrorText error={docs.error} />
        </>
      }
    >
      {docs.isPending ? (
        <Loading />
      ) : data && data.documents.length === 0 ? (
        <p className="p-6 text-center text-sm text-muted-foreground">{filter ? "No document matches." : "This collection is empty."}</p>
      ) : (
        data && (
          <ol className="divide-y" aria-label="Documents">
            {data.documents.map((d, i) => (
              <Document key={page * size + i} json={d} cut={data.truncated.includes(i)} />
            ))}
          </ol>
        )
      )}
    </BrowserShell>
  );
}

/** A filter or sort as Extended JSON, applied on Enter. */
function JsonField({
  icon,
  label,
  placeholder,
  value,
  onApply,
  className,
}: {
  icon: ReactNode;
  label: string;
  placeholder: string;
  value: string;
  onApply: (v: string) => void;
  className?: string;
}) {
  const [text, setText] = useState(value);
  const dirty = text.trim() !== value;
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onApply(text.trim());
      }}
      className={cn(
        "flex h-8 items-center gap-1.5 rounded-md border bg-background px-2 focus-within:border-ring focus-within:ring-[3px] focus-within:ring-ring/50 [&_svg]:size-3.5 [&_svg]:text-muted-foreground",
        dirty && "border-dashed",
        className,
      )}
      title={`${label}: Extended JSON, applied with Enter`}
    >
      {icon}
      <input
        aria-label={label}
        placeholder={placeholder}
        value={text}
        spellCheck={false}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => e.key === "Escape" && setText(value)}
        className="min-w-0 flex-1 bg-transparent font-mono text-xs outline-none placeholder:text-muted-foreground/60"
      />
    </form>
  );
}

/** One document, indented; a cut one stays as the server sent it. */
function Document({ json, cut }: { json: string; cut: boolean }) {
  let pretty = json;
  if (!cut) {
    try {
      pretty = JSON.stringify(JSON.parse(json), null, 2);
    } catch {
      // Shown as sent.
    }
  }
  return (
    <li className="group relative px-4 py-3">
      <pre className="overflow-x-auto font-mono text-xs leading-relaxed whitespace-pre-wrap">{pretty}</pre>
      {cut && <p className="mt-1 text-xs text-muted-foreground">Cut at 4 KiB: run a projection in the console to see the rest.</p>}
      <span className="absolute top-2 right-2 opacity-0 transition-opacity group-hover:opacity-100 focus-within:opacity-100">
        <CopyButton value={json} label="Copy as JSON" />
      </span>
    </li>
  );
}
