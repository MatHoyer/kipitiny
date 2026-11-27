import { useInfiniteQuery } from "@tanstack/react-query";
import { Plus, RefreshCw } from "lucide-react";
import { useMemo, useRef, useState } from "react";
import { ErrorText } from "@/components/common";
import { Loading } from "@kipit-labs/kipitidy/loading";
import { Button } from "@kipit-labs/kipitidy/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@kipit-labs/kipitidy/select";
import { cn } from "@/lib/utils";
import { api, type RedisKey } from "../../api";
import { BrowserShell, SidebarNav, SearchBox, SidebarToggle } from "./browser-shell";
import { useUnsaved } from "./changes";
import { KeyTree } from "./redis-key-tree";
import { NewKeyDialog } from "./redis-new-key";
import { ValueView } from "./redis-value-view";
import { WriteMode } from "./write-mode";

/** Keys to gather per scan: a SCAN step may return few or none. */
const WANT = 200;

/** A bare word matches anywhere in the key; a glob (*, ?, [) is used as is. */
const toPattern = (s: string) => (!s.trim() ? "*" : /[*?[]/.test(s) ? s.trim() : `*${s.trim()}*`);

export function RedisBrowser({ serviceId }: { serviceId: string }) {
  const [match, setMatch] = useState("");
  const [picked, setPicked] = useState<string | null>(null);
  const [sidebar, setSidebar] = useState(true);
  const navScroll = useRef(0);
  // Kept across keys: switching doesn't lock it again.
  const [write, setWrite] = useState(false);
  const { confirm } = useUnsaved();
  const pick = (key: string) => confirm(() => setPicked(key));
  const [creating, setCreating] = useState(false);
  const pattern = toPattern(match);
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
  const list = useMemo(() => (keys.data?.pages.flatMap((p) => p.keys) ?? []).sort((a, b) => a.key.localeCompare(b.key)), [keys.data]);
  const current = picked ?? list[0]?.key ?? null;

  const nav = (
    <>
      <div className="flex h-12 shrink-0 items-center gap-1 border-b px-2">
        <span className="flex-1 px-1 text-sm font-medium">Keys</span>
        {write && (
          <Button variant="ghost" size="icon-xs" title="New key" onClick={() => confirm(() => setCreating(true))}>
            <Plus />
          </Button>
        )}
        <Button variant="ghost" size="icon-xs" title="Rescan" onClick={() => keys.refetch()}>
          <RefreshCw className={cn(keys.isFetching && "animate-spin")} />
        </Button>
      </div>
      <div className="border-b p-2">
        <SearchBox value={match} onChange={setMatch} placeholder="Match keys" />
      </div>
      <SidebarNav scroll={navScroll} aria-label="Keys" className="flex-1 overflow-y-auto p-2">
        {keys.isPending ? (
          <Loading className="py-6" />
        ) : list.length === 0 ? (
          <p className="px-2 text-xs text-muted-foreground">{pattern === "*" ? "No keys yet." : `No key matches ${pattern}.`}</p>
        ) : (
          <KeyTree keys={list} picked={current} onPick={pick} />
        )}
        <ErrorText error={keys.error} />
      </SidebarNav>
      <div className="flex items-center gap-2 border-t px-3 py-2 text-xs text-muted-foreground">
        <span className="flex-1">
          {list.length} keys{keys.hasNextPage && " so far"}
        </span>
        {keys.hasNextPage && (
          <Button variant="outline" size="xs" loading={keys.isFetchingNextPage} onClick={() => keys.fetchNextPage()}>
            Scan further
          </Button>
        )}
      </div>
    </>
  );

  const picker = (
    <Select value={current ?? ""} onValueChange={pick}>
      <SelectTrigger size="sm" aria-label="Key" className="max-w-48 font-mono text-xs md:hidden">
        <SelectValue placeholder="Pick a key" />
      </SelectTrigger>
      <SelectContent>
        {list.map((k) => (
          <SelectItem key={k.key} value={k.key} className="font-mono text-xs">
            {k.key}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );

  const writeSwitch = (
    <WriteMode
      write={write}
      onChange={(w) => (w ? setWrite(true) : confirm(() => setWrite(false)))}
      description="You can then add keys, and edit, add and delete items or whole keys. Nothing is written until you review the commands and save; only a restore from a backup undoes a save."
    />
  );
  const dialog = (
    <NewKeyDialog
      open={creating}
      onOpenChange={setCreating}
      serviceId={serviceId}
      onCreated={(key) => {
        setPicked(key);
        keys.refetch();
      }}
    />
  );

  if (!current) {
    return (
      <BrowserShell
        sidebar={nav}
        sidebarOpen={sidebar}
        write={write}
        toolbar={
          <>
            <SidebarToggle open={sidebar} onToggle={() => setSidebar(!sidebar)} />
            <span className="ml-auto flex items-center gap-1.5">{writeSwitch}</span>
            {dialog}
          </>
        }
      >
        <p className="p-6 text-center text-sm text-muted-foreground">{keys.isPending ? "" : write ? "No keys yet: + in the sidebar adds one." : "Nothing to show."}</p>
      </BrowserShell>
    );
  }
  return (
    <ValueView
      key={current}
      serviceId={serviceId}
      name={current}
      redisKey={list.find((k) => k.key === current)}
      sidebar={nav}
      sidebarOpen={sidebar}
      write={write}
      toolbarStart={
        <>
          <SidebarToggle open={sidebar} onToggle={() => setSidebar(!sidebar)} />
          {picker}
        </>
      }
      toolbarEnd={
        <>
          {writeSwitch}
          {dialog}
        </>
      }
    />
  );
}
