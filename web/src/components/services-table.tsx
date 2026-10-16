import { ArrowDown, ArrowUp, Play, RotateCw, Search, Square, Trash2 } from "lucide-react";
import { useState, type ReactNode } from "react";
import { Link, useNavigate } from "react-router";
import { databaseLabels } from "@/components/brand-icons";
import { BulkBar, useBulk, useSelection } from "@/components/bulk";
import { StateBadge, stateColors } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { ServiceIcon } from "@/components/service-icon";
import { TagChips, TagFilter, type useTagFilter } from "@/components/tags";
import { needsAttention } from "@/components/topology";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { liveState } from "@/lib/format";
import { cn } from "@/lib/utils";
import { api, isDatabase, type Project, type Service, type ServiceKind } from "../api";

type SortKey = "name" | "project" | "kind" | "state";

// The dots match StateBadge's; stopped has no color there either.
const stateFilters = [
  ["running", "Running", stateColors.running],
  ["attention", "Need attention", stateColors.failed],
  ["asleep", "Asleep", stateColors.asleep],
  ["stopped", "Stopped", "bg-muted-foreground/50"],
] as const;

const kindLabel = (k: ServiceKind) => (isDatabase(k) ? databaseLabels[k] : "App");

/** Every service of the listed projects in one table, filtered by tag, kind, state and name. */
export function ServicesTable({
  projects,
  services,
  filter,
  viewSwitch,
}: {
  projects: Project[];
  services: Service[];
  filter: ReturnType<typeof useTagFilter>;
  /** Leads the filters row. */
  viewSwitch: ReactNode;
}) {
  const navigate = useNavigate();
  const [q, setQ] = useState("");
  const [kind, setKind] = useState("all");
  const [state, setState] = useState("all");
  const [sort, setSort] = useState<{ key: SortKey; desc: boolean }>({ key: "name", desc: false });
  const projectName = (id: string) => projects.find((p) => p.id === id)?.name ?? "";
  const kinds = [...new Set(services.map((s) => s.kind))].sort();

  const needle = q.trim().toLowerCase();
  const rows = services
    .map((s) => ({ s, project: projectName(s.projectId), state: liveState(s) }))
    .filter(
      ({ s, project, state: st }) =>
        filter.matches(s.tags) &&
        (kind === "all" || s.kind === kind) &&
        (state === "all" || (state === "attention" ? needsAttention(s) : st === state)) &&
        (!needle || `${s.name} ${project} ${s.image} ${s.domain}`.toLowerCase().includes(needle)),
    );
  const value = (r: (typeof rows)[number]) =>
    sort.key === "name" ? r.s.name : sort.key === "project" ? r.project : sort.key === "kind" ? kindLabel(r.s.kind) : r.state;
  rows.sort((a, b) => (sort.desc ? -1 : 1) * value(a).localeCompare(value(b)) || a.s.name.localeCompare(b.s.name));
  const sel = useSelection(rows.map((r) => r.s));
  const bulk = useBulk<Service>(sel.clear);
  const act = (label: string, a: "start" | "stop" | "restart") => bulk.mutate({ label, items: sel.chosen, run: (s) => api.serviceAction(s.id, a) });
  const n = sel.chosen.length;

  const header = (key: SortKey, label: string, className?: string) => (
    <th scope="col" aria-sort={sort.key === key ? (sort.desc ? "descending" : "ascending") : undefined} className={cn("border-b px-3 py-2.5 font-normal", className)}>
      <button
        type="button"
        onClick={() => setSort({ key, desc: sort.key === key && !sort.desc })}
        className="inline-flex items-center gap-1 hover:text-foreground"
      >
        {label}
        {sort.key === key && (sort.desc ? <ArrowDown className="size-3" /> : <ArrowUp className="size-3" />)}
      </button>
    </th>
  );

  return (
    <>
      <div className="flex flex-wrap items-center gap-2">
        {viewSwitch}
        <div className="relative min-w-48 flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search services" aria-label="Search" className="pl-9" />
        </div>
        <Select value={kind} onValueChange={setKind}>
          <SelectTrigger aria-label="Kind" className="min-w-32">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">Any kind</SelectItem>
            {kinds.map((k) => (
              <SelectItem key={k} value={k}>
                <ServiceIcon service={{ kind: k }} />
                {kindLabel(k)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select value={state} onValueChange={setState}>
          <SelectTrigger aria-label="State" className="min-w-36">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">Any state</SelectItem>
            {stateFilters.map(([value, label, dot]) => (
              <SelectItem key={value} value={value}>
                <span className={cn("size-2 rounded-full", dot)} />
                {label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <TagFilter items={services.map((s) => s.tags)} filter={filter} />
      </div>
      {rows.length === 0 ? (
        <p className="py-6 text-center text-sm text-muted-foreground">No service matches these filters.</p>
      ) : (
        <div className="overflow-x-auto rounded-lg border bg-card">
          <table className="w-full border-separate border-spacing-0 text-sm">
            <thead className="text-left text-xs text-muted-foreground">
              <tr>
                <th scope="col" className="w-0 border-b py-2.5 pl-4">
                  <Checkbox checked={sel.all} onCheckedChange={(on) => sel.setAll(on === true)} aria-label="Select all shown services" />
                </th>
                {header("name", "Service", "w-full")}
                {header("project", "Project")}
                {header("kind", "Kind", "hidden sm:table-cell")}
                {header("state", "State")}
                <th scope="col" className="hidden border-b px-3 py-2.5 font-normal md:table-cell">
                  Domain
                </th>
                <th scope="col" className="hidden border-b px-3 py-2.5 font-normal lg:table-cell">
                  Tags
                </th>
              </tr>
            </thead>
            <tbody>
              {rows.map(({ s, project, state: st }) => (
                // The whole row opens the service; the name is its keyboard target.
                <tr key={s.id} onClick={() => navigate(`/services/${s.id}`)} className="cursor-pointer hover:bg-muted/60">
                  <td className="border-b py-2.5 pl-4" onClick={(e) => e.stopPropagation()}>
                    <Checkbox checked={sel.has(s.id)} onCheckedChange={(on) => sel.toggle(s.id, on === true)} aria-label={`Select ${s.name}`} />
                  </td>
                  <td className="max-w-0 border-b px-3 py-2.5">
                    <Link to={`/services/${s.id}`} onClick={(e) => e.stopPropagation()} className="flex items-center gap-2.5 outline-none focus-visible:underline">
                      <ServiceIcon service={s} className="size-4 shrink-0 text-muted-foreground" />
                      <span className="min-w-0">
                        <span className="block truncate font-medium">{s.name}</span>
                        <span className="block truncate font-mono text-xs text-muted-foreground">{s.image}</span>
                      </span>
                    </Link>
                  </td>
                  <td className="border-b px-3 py-2.5 whitespace-nowrap">{project}</td>
                  <td className="hidden border-b px-3 py-2.5 whitespace-nowrap text-muted-foreground sm:table-cell">{kindLabel(s.kind)}</td>
                  <td className="border-b px-3 py-2.5">
                    <StateBadge state={st} />
                  </td>
                  <td className="hidden max-w-48 truncate border-b px-3 py-2.5 font-mono text-xs text-muted-foreground md:table-cell">{s.domain}</td>
                  <td className="hidden border-b px-3 py-2.5 lg:table-cell">
                    <TagChips tags={s.tags} max={2} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <BulkBar count={n} noun="service" onClear={sel.clear}>
        <Button size="sm" variant="outline" disabled={bulk.isPending} onClick={() => act("Start", "start")}>
          <Play data-icon="inline-start" />
          Start
        </Button>
        <Button size="sm" variant="outline" disabled={bulk.isPending} onClick={() => act("Stop", "stop")}>
          <Square data-icon="inline-start" />
          Stop
        </Button>
        <Button size="sm" variant="outline" disabled={bulk.isPending} onClick={() => act("Restart", "restart")}>
          <RotateCw data-icon="inline-start" />
          Restart
        </Button>
        <ConfirmDialog
          trigger={
            <Button size="sm" variant="destructive" disabled={bulk.isPending}>
              <Trash2 data-icon="inline-start" />
              Delete
            </Button>
          }
          title={`Delete ${n} service${n === 1 ? "" : "s"}?`}
          description={`${sel.chosen.map((s) => s.name).join(", ")}: their containers and volumes go, data included.`}
          typeToConfirm={`delete ${n}`}
          onConfirm={() => bulk.mutate({ label: "Delete", items: sel.chosen, run: (s) => api.deleteService(s.id, s.name) })}
        />
      </BulkBar>
    </>
  );
}
