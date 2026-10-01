import { useQuery } from "@tanstack/react-query";
import { Search } from "lucide-react";
import { useState } from "react";
import { ErrorText } from "@/components/common";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { timeAgo } from "@/lib/format";
import { cn } from "@/lib/utils";
import { api, type AuditEntry } from "@/api";
import { SettingsPage } from "./page";

type Result = "all" | "ok" | "failed";

// One grid for the header row and the entries: on phones, who and when fold under the action.
const cols = "grid grid-cols-[1fr_auto] items-baseline gap-x-4 sm:grid-cols-[6rem_10rem_1fr_4.5rem]";

export function Audit() {
  const audit = useQuery({ queryKey: ["audit"], queryFn: api.audit, refetchInterval: 15_000 });
  const [q, setQ] = useState("");
  const [actor, setActor] = useState("all");
  const [result, setResult] = useState<Result>("all");
  const actors = [...new Set(audit.data?.map((e) => e.actor))].sort();
  const needle = q.trim().toLowerCase();
  const rows = audit.data?.filter(
    (e) =>
      (actor === "all" || e.actor === actor) &&
      (result === "all" || (result === "failed") === e.status >= 400) &&
      (!needle || `${e.action} ${e.target} ${e.error ?? ""}`.toLowerCase().includes(needle)),
  );

  return (
    <SettingsPage>
      {/* Sticks under the page header; bleeds to the page edges. */}
      <div className="sticky top-14 z-10 -mx-4 -mt-6 border-b bg-background/95 px-4 pt-4 backdrop-blur sm:-mx-6 sm:px-6 lg:-mx-8 lg:px-8">
        <div className="flex flex-wrap items-center gap-2">
          <div className="relative min-w-48 flex-1">
            <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder="Search actions, targets and errors"
              aria-label="Search"
              className="pl-9"
            />
          </div>
          <Select value={actor} onValueChange={setActor}>
            <SelectTrigger aria-label="Who" className="min-w-36">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">Everyone</SelectItem>
              {actors.map((a) => (
                <SelectItem key={a} value={a}>
                  {a}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Select value={result} onValueChange={(v) => setResult(v as Result)}>
            <SelectTrigger aria-label="Result" className="min-w-32">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">Any result</SelectItem>
              <SelectItem value="ok">Succeeded</SelectItem>
              <SelectItem value="failed">Failed</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div className={cn(cols, "mt-3 py-2 text-xs font-medium text-muted-foreground")}>
          <span className="max-sm:hidden">When</span>
          <span className="max-sm:hidden">Who</span>
          <span>
            Action
            {rows && audit.data && (
              <span className="ml-2 font-normal">
                {rows.length === audit.data.length ? `${rows.length} entries` : `${rows.length} of ${audit.data.length}`}
              </span>
            )}
          </span>
          <span className="text-right">Result</span>
        </div>
      </div>

      <ErrorText error={audit.error} />
      {rows?.length === 0 ? (
        <p className="py-12 text-center text-sm text-muted-foreground">
          {audit.data?.length ? "Nothing matches these filters." : "Nothing yet."}
        </p>
      ) : (
        <ul className="-mt-6 divide-y text-sm">
          {rows?.map((e) => (
            <AuditRow key={e.id} entry={e} />
          ))}
        </ul>
      )}
      {audit.data && audit.data.length > 0 && (
        <p className="text-center text-xs text-muted-foreground">The last {audit.data.length} changes are kept here.</p>
      )}
    </SettingsPage>
  );
}

function AuditRow({ entry: e }: { entry: AuditEntry }) {
  const when = (
    <span title={new Date(e.createdAt).toLocaleString()} className="whitespace-nowrap">
      {timeAgo(e.createdAt)}
    </span>
  );
  return (
    <li className={cn(cols, "py-3")}>
      <span className="text-muted-foreground max-sm:hidden">{when}</span>
      <span className="truncate max-sm:hidden">{e.actor}</span>
      <div className="min-w-0">
        <p className="font-mono text-xs break-all">
          {e.action}
          {e.target && <span className="text-muted-foreground"> {e.target}</span>}
        </p>
        {e.error && <p className="mt-1 text-xs text-destructive">{e.error}</p>}
        <p className="mt-0.5 text-xs text-muted-foreground sm:hidden">
          {e.actor} · {when}
        </p>
      </div>
      <span className="text-right">
        <span
          className={cn(
            "rounded-full px-2 py-0.5 font-mono text-xs",
            e.status >= 400
              ? "bg-destructive/10 text-destructive"
              : "bg-emerald-500/10 text-emerald-700 dark:text-emerald-400",
          )}
        >
          {e.status}
        </span>
      </span>
    </li>
  );
}
