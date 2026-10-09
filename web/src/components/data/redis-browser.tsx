import { useInfiniteQuery } from "@tanstack/react-query";
import { RefreshCw, Search } from "lucide-react";
import { useState } from "react";
import { Empty, ErrorText, Loading, Tag } from "@/components/common";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { formatBytes, formatDuration } from "@/lib/format";
import { cn } from "@/lib/utils";
import { api, type RedisKey } from "../../api";
import { DataTable, type DataColumn } from "./data-table";

/** Keys to gather per "load more": a SCAN step may return few or none. */
const WANT = 100;

const valueColumns: Record<string, DataColumn[]> = {
  string: [{ name: "value" }],
  hash: [{ name: "field" }, { name: "value" }],
  list: [{ name: "index" }, { name: "value" }],
  set: [{ name: "member" }],
  zset: [{ name: "member" }, { name: "score" }],
};

export function RedisBrowser({ serviceId }: { serviceId: string }) {
  const [draft, setDraft] = useState("");
  const [pattern, setPattern] = useState("*");
  const [picked, setPicked] = useState<string | null>(null);
  const keys = useInfiniteQuery({
    queryKey: ["data", serviceId, "keys", pattern],
    initialPageParam: "0",
    // Gather SCAN steps until WANT keys or the end, so a sparse pattern still fills a page.
    queryFn: async ({ pageParam }) => {
      const out: RedisKey[] = [];
      let cursor = pageParam;
      do {
        const page = await api.redisScan(serviceId, cursor, pattern);
        out.push(...page.keys);
        cursor = page.cursor;
      } while (cursor !== "0" && out.length < WANT);
      return { keys: out, cursor };
    },
    getNextPageParam: (last) => (last.cursor === "0" ? undefined : last.cursor),
  });
  const list = keys.data?.pages.flatMap((p) => p.keys) ?? [];

  return (
    <div className="grid gap-4 md:grid-cols-[18rem_minmax(0,1fr)]">
      <div className="min-w-0 space-y-3 md:border-r md:pr-3">
        <form
          className="flex gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            setPattern(draft.trim() || "*");
            setPicked(null);
          }}
        >
          <Input aria-label="Key pattern" placeholder="pattern, e.g. user:*" value={draft} onChange={(e) => setDraft(e.target.value)} className="h-8" />
          <Button type="submit" variant="outline" size="icon-sm" title="Search">
            <Search />
          </Button>
          <Button type="button" variant="ghost" size="icon-sm" title="Refresh" onClick={() => keys.refetch()}>
            <RefreshCw className={cn(keys.isFetching && "animate-spin")} />
          </Button>
        </form>
        <ErrorText error={keys.error} />
        {keys.isPending ? (
          <Loading />
        ) : list.length === 0 ? (
          <Empty>{pattern === "*" ? "No keys." : "No keys match."}</Empty>
        ) : (
          <ul aria-label="Keys" className="max-h-[65vh] space-y-0.5 overflow-y-auto">
            {list.map((k) => (
              <li key={k.key}>
                <button
                  type="button"
                  onClick={() => setPicked(k.key)}
                  aria-current={picked === k.key}
                  className="flex w-full items-center gap-2 rounded-md px-2 py-1 text-left text-sm hover:bg-muted aria-[current=true]:bg-muted"
                >
                  <span className="min-w-0 flex-1 truncate font-mono text-xs">{k.key}</span>
                  <Tag>{k.type}</Tag>
                </button>
              </li>
            ))}
          </ul>
        )}
        {keys.hasNextPage && (
          <Button variant="outline" size="sm" className="w-full" loading={keys.isFetchingNextPage} onClick={() => keys.fetchNextPage()}>
            Load more
          </Button>
        )}
      </div>
      {picked ? (
        <ValueView key={picked} serviceId={serviceId} redisKey={list.find((k) => k.key === picked)} name={picked} />
      ) : (
        <Empty>Pick a key to see its value.</Empty>
      )}
    </div>
  );
}

function ValueView({ serviceId, name, redisKey }: { serviceId: string; name: string; redisKey?: RedisKey }) {
  const pages = useInfiniteQuery({
    queryKey: ["data", serviceId, "key", name],
    initialPageParam: "0",
    queryFn: ({ pageParam }) => api.redisGet(serviceId, name, pageParam),
    getNextPageParam: (last) => last.cursor || undefined,
  });
  if (pages.isPending) return <Loading />;
  if (pages.error) return <ErrorText error={pages.error} />;
  const first = pages.data.pages[0];
  const items: (string | null)[][] = [];
  const truncated: [number, number][] = [];
  for (const p of pages.data.pages) {
    truncated.push(...p.truncated.map(([r, c]): [number, number] => [r + items.length, c]));
    items.push(...p.items);
  }
  const columns = first.type === "stream" ? streamColumns(items) : (valueColumns[first.type] ?? [{ name: "value" }]);
  // Stream entries differ in field count: pad to the widest.
  const rows = items.map((it) => (it.length < columns.length ? [...it, ...Array<null>(columns.length - it.length).fill(null)] : it));

  return (
    <div className="min-w-0 space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="min-w-0">
          <h3 className="truncate font-mono text-sm font-medium">{name}</h3>
          <p className="text-xs text-muted-foreground">
            {first.type} · {first.type === "string" ? formatBytes(first.length) : `${first.length} items`}
            {redisKey && redisKey.bytes > 0 && ` · ${formatBytes(redisKey.bytes)} in memory`}
            {" · "}
            {first.ttl < 0 ? "no expiry" : `expires in ${formatDuration(first.ttl)}`}
          </p>
        </div>
        <Button variant="ghost" size="icon-sm" title="Refresh" onClick={() => pages.refetch()}>
          <RefreshCw className={cn(pages.isFetching && "animate-spin")} />
        </Button>
      </div>
      <DataTable columns={columns} rows={rows} truncated={truncated} empty="Empty." />
      {pages.hasNextPage && (
        <Button variant="outline" size="sm" loading={pages.isFetchingNextPage} onClick={() => pages.fetchNextPage()}>
          Load more
        </Button>
      )}
    </div>
  );
}

/** A stream's entries are [id, field, value, ...]: one column per field seen. */
function streamColumns(items: (string | null)[][]): DataColumn[] {
  const width = Math.max(1, ...items.map((it) => it.length));
  const cols: DataColumn[] = [{ name: "id" }];
  for (let i = 1; i < width; i += 2) cols.push({ name: `field ${(i + 1) / 2}` }, { name: `value ${(i + 1) / 2}` });
  return cols;
}
