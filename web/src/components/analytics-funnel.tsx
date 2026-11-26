import { useQuery } from "@tanstack/react-query";
import { Plus, X } from "lucide-react";
import { useState, type FormEvent } from "react";
import { Empty, ErrorText } from "@/components/common";
import { Loading } from "@kipit-labs/kipitidy/loading";
import { Section } from "@kipit-labs/kipitidy/section";
import { Button } from "@kipit-labs/kipitidy/button";
import { Input } from "@kipit-labs/kipitidy/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@kipit-labs/kipitidy/select";
import { api, type Service } from "../api";

type Step = { kind: "page" | "event"; value: string };

// shortcut: funnels are kept in this browser only; store them on the app if they should be shared between users.
const storageKey = (id: string) => `kipitiny.funnel.${id}`;
const load = (id: string): Step[] => {
  try {
    const v = JSON.parse(localStorage.getItem(storageKey(id)) ?? "[]");
    return Array.isArray(v) ? v : [];
  } catch {
    return [];
  }
};
const keep = (id: string, steps: Step[]) => {
  try {
    localStorage.setItem(storageKey(id), JSON.stringify(steps));
  } catch {
    // Private mode: the funnel lasts until the page closes.
  }
};

const pct = (n: number, of: number) => (of ? `${Math.round((n / of) * 100)}%` : "—");

/** Visits through ordered steps (pages or custom events) over the range shown. */
export function Funnel({ svc, span }: { svc: Service; span: number }) {
  const [steps, setSteps] = useState<Step[]>(() => load(svc.id));
  const [draft, setDraft] = useState<Step[]>(() => (steps.length ? steps : [{ kind: "page", value: "/" }, { kind: "event", value: "" }]));
  const ready = steps.length >= 2;
  const funnel = useQuery({
    queryKey: ["service-funnel", svc.id, span, steps],
    queryFn: () => {
      const to = Math.ceil(Date.now() / 1000 / 60) * 60;
      return api.serviceFunnel(svc.id, { from: to - span, to, steps: steps.map((s) => `${s.kind}:${s.value}`) });
    },
    enabled: ready,
    placeholderData: (prev) => prev,
    refetchInterval: 60_000,
  });
  const set = (i: number, s: Partial<Step>) => setDraft(draft.map((d, j) => (j === i ? { ...d, ...s } : d)));
  const valid = draft.length >= 2 && draft.every((d) => d.value.trim());
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    const next = draft.map((d) => ({ ...d, value: d.value.trim() }));
    setSteps(next);
    keep(svc.id, next);
  };
  const first = funnel.data?.[0]?.visits ?? 0;
  return (
    <Section
      title="Funnel"
      description="How many of the range's visits went through these steps in order, each after the one before within the visit. Filters don't apply."
    >
      <form className="space-y-2" onSubmit={onSubmit}>
        {draft.map((d, i) => (
          <div key={i} className="flex items-center gap-2">
            <span className="w-5 shrink-0 text-right text-sm text-muted-foreground tabular-nums">{i + 1}.</span>
            <Select value={d.kind} onValueChange={(kind) => set(i, { kind: kind as Step["kind"] })}>
              <SelectTrigger aria-label={`Step ${i + 1} kind`} className="w-28 shrink-0">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="page">Page</SelectItem>
                <SelectItem value="event">Event</SelectItem>
              </SelectContent>
            </Select>
            <Input
              aria-label={`Step ${i + 1}`}
              placeholder={d.kind === "page" ? "/pricing" : "signup"}
              value={d.value}
              onChange={(e) => set(i, { value: e.target.value })}
            />
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              aria-label={`Remove step ${i + 1}`}
              disabled={draft.length <= 2}
              onClick={() => setDraft(draft.filter((_, j) => j !== i))}
            >
              <X />
            </Button>
          </div>
        ))}
        <div className="flex gap-2">
          <Button type="button" variant="ghost" size="sm" disabled={draft.length >= 8} onClick={() => setDraft([...draft, { kind: "page", value: "" }])}>
            <Plus />
            Add step
          </Button>
          <Button type="submit" variant="outline" size="sm" disabled={!valid}>
            Show funnel
          </Button>
        </div>
      </form>
      {!ready ? (
        <Empty>Pick two steps or more, e.g. the home page, then /pricing, then a signup event.</Empty>
      ) : funnel.isPending ? (
        <Loading />
      ) : funnel.error ? (
        <ErrorText error={funnel.error} />
      ) : (
        <ol className="space-y-2">
          {funnel.data.map((f, i) => {
            const [kind, ...rest] = f.step.split(":");
            const prev = i ? funnel.data[i - 1].visits : f.visits;
            return (
              <li key={i} className="space-y-1">
                <div className="flex items-baseline justify-between gap-2 text-sm">
                  <span className="truncate">
                    <span className="text-muted-foreground">
                      {i + 1}. {kind === "page" ? "Page" : "Event"}
                    </span>{" "}
                    {rest.join(":")}
                  </span>
                  <span className="shrink-0 tabular-nums">
                    {f.visits} visits
                    {i > 0 && (
                      <span className="text-muted-foreground">
                        {" "}
                        · {pct(f.visits, prev)} of step {i} · {pct(f.visits, first)} overall
                      </span>
                    )}
                  </span>
                </div>
                <div className="h-2 rounded-full bg-muted">
                  <div className="h-2 rounded-full bg-primary" style={{ width: first ? `${(f.visits / first) * 100}%` : 0 }} />
                </div>
              </li>
            );
          })}
        </ol>
      )}
    </Section>
  );
}
