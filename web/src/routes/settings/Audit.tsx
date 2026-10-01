import { useQuery } from "@tanstack/react-query";
import { Search } from "lucide-react";
import { useState } from "react";
import { CheckboxField, Empty, ErrorText, Section } from "@/components/common";
import { Input } from "@/components/ui/input";
import { timeAgo } from "@/lib/format";
import { cn } from "@/lib/utils";
import { api } from "@/api";

export function Audit() {
  const audit = useQuery({ queryKey: ["audit"], queryFn: api.audit, refetchInterval: 15_000 });
  const [q, setQ] = useState("");
  const [failures, setFailures] = useState(false);
  const needle = q.trim().toLowerCase();
  const rows = audit.data?.filter(
    (e) =>
      (!failures || e.status >= 400) &&
      (!needle || `${e.actor} ${e.action} ${e.target}`.toLowerCase().includes(needle)),
  );
  const th = "px-2 pb-2 font-medium";

  return (
    <Section title="Audit log" description="Every change, by whom, and how it went. The last 200 are shown.">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <div className="relative min-w-48 flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Filter by who, action or target"
            aria-label="Filter"
            className="pl-8"
          />
        </div>
        <CheckboxField label="Failures only" checked={failures} onCheckedChange={setFailures} />
      </div>
      <ErrorText error={audit.error} />
      {rows?.length === 0 ? (
        <Empty>{audit.data?.length ? "Nothing matches." : "Nothing yet."}</Empty>
      ) : (
        <div className="-mx-2 overflow-x-auto">
          <table className="w-full text-left text-xs">
            <thead className="text-muted-foreground">
              <tr className="border-b">
                <th className={th}>When</th>
                <th className={th}>Who</th>
                <th className={th}>Action</th>
                <th className={cn(th, "text-right")}>Result</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {rows?.map((e) => (
                <tr key={e.id} className="hover:bg-muted/50">
                  <td className="px-2 py-2 whitespace-nowrap text-muted-foreground" title={new Date(e.createdAt).toLocaleString()}>
                    {timeAgo(e.createdAt)}
                  </td>
                  <td className="px-2 py-2">{e.actor}</td>
                  <td className="px-2 py-2 font-mono">
                    {e.action}
                    {e.target && <span className="text-muted-foreground"> {e.target}</span>}
                    {e.error && <p className="mt-0.5 font-sans text-destructive">{e.error}</p>}
                  </td>
                  <td className="px-2 py-2 text-right">
                    <span
                      className={cn(
                        "rounded-full px-2 py-0.5 font-mono",
                        e.status >= 400
                          ? "bg-destructive/10 text-destructive"
                          : "bg-emerald-500/10 text-emerald-700 dark:text-emerald-400",
                      )}
                    >
                      {e.status}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Section>
  );
}
