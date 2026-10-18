import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { BarChart3, Image, Link2, Pencil, Trash2 } from "lucide-react";
import { useState, type FormEvent } from "react";
import { toast } from "sonner";
import { AnalyticsPanel } from "@/components/analytics-tab";
import { CopyButton, Empty, ErrorText, Loading, Mono, Section } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { api, type Tracker, type TrackerKind } from "@/api";

const utmKeys = ["utm_source", "utm_medium", "utm_campaign"] as const;

/** Pixels and tracked links, on the analytics domain. */
export function Trackers() {
  const qc = useQueryClient();
  const trackers = useQuery({ queryKey: ["trackers"], queryFn: api.trackers });
  const [editing, setEditing] = useState<Tracker | TrackerKind | null>(null);
  const [stats, setStats] = useState<Tracker | null>(null);
  const remove = useMutation({
    meta: { error: "Couldn't delete it" },
    mutationFn: (t: Tracker) => api.deleteTracker(t.id),
    onSuccess: (_, t) => {
      qc.invalidateQueries({ queryKey: ["trackers"] });
      toast.success(`${t.name} deleted`);
    },
  });
  return (
    <Section
      title="Pixels and links"
      description="Count what the script can't see: a pixel is an image whose loads are views (emails, README badges, pages you don't control); a link redirects to its target and counts the clicks."
      actions={
        <>
          <Button variant="outline" size="sm" onClick={() => setEditing("pixel")}>
            <Image data-icon="inline-start" />
            New pixel
          </Button>
          <Button variant="outline" size="sm" onClick={() => setEditing("link")}>
            <Link2 data-icon="inline-start" />
            New link
          </Button>
        </>
      }
    >
      {trackers.isPending ? (
        <Loading />
      ) : trackers.error ? (
        <ErrorText error={trackers.error} />
      ) : trackers.data.length === 0 ? (
        <Empty>No pixel or link yet.</Empty>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="text-xs text-muted-foreground">
              <tr className="text-left">
                <th className="pb-1.5 font-normal">Name</th>
                <th className="pb-1.5 font-normal">URL</th>
                <th className="pb-1.5 text-right font-normal whitespace-nowrap">30 days</th>
                <th className="pb-1.5" />
              </tr>
            </thead>
            <tbody>
              {trackers.data.map((t) => (
                <tr key={t.id} className="border-t align-top">
                  <td className="py-2 pr-3">
                    <span className="flex items-center gap-1.5 font-medium [&>svg]:size-3.5 [&>svg]:shrink-0 [&>svg]:text-muted-foreground">
                      {t.kind === "link" ? <Link2 /> : <Image />}
                      {t.name}
                    </span>
                    {t.target && <p className="max-w-72 truncate text-xs text-muted-foreground" title={t.target}>→ {t.target}</p>}
                  </td>
                  <td className="py-2 pr-3">
                    <span className="flex items-center gap-1">
                      <Mono>{t.url}</Mono>
                      <CopyButton value={t.kind === "pixel" ? `<img src="${t.url}" width="1" height="1" alt="">` : t.url} />
                    </span>
                  </td>
                  <td className="py-2 text-right tabular-nums">
                    {t.hits} {t.kind === "link" ? "click" : "view"}
                    {t.hits === 1 ? "" : "s"}
                  </td>
                  <td className="py-1 pl-2 text-right whitespace-nowrap">
                    <Button variant="ghost" size="icon-sm" aria-label={`Stats of ${t.name}`} onClick={() => setStats(t)}>
                      <BarChart3 />
                    </Button>
                    <Button variant="ghost" size="icon-sm" aria-label={`Edit ${t.name}`} onClick={() => setEditing(t)}>
                      <Pencil />
                    </Button>
                    <ConfirmDialog
                      trigger={
                        <Button variant="ghost" size="icon-sm" aria-label={`Delete ${t.name}`}>
                          <Trash2 />
                        </Button>
                      }
                      title={`Delete ${t.name}?`}
                      description={
                        t.kind === "link"
                          ? "Its URL stops redirecting (404) and its counted clicks are deleted."
                          : "Its image stops counting and its counted views are deleted."
                      }
                      onConfirm={() => remove.mutate(t)}
                    />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {editing && <TrackerDialog initial={editing} onClose={() => setEditing(null)} />}
      <Sheet open={!!stats} onOpenChange={(o) => !o && setStats(null)}>
        <SheetContent className="w-full overflow-y-auto data-[side=right]:sm:max-w-4xl">
          {stats && (
            <>
              <SheetHeader>
                <SheetTitle>{stats.name}</SheetTitle>
                <SheetDescription>{stats.kind === "link" ? "Clicks of this link." : "Views of this pixel."}</SheetDescription>
              </SheetHeader>
              <div className="px-4 pb-6">
                <AnalyticsPanel
                  queryKey={["tracker-analytics", stats.id]}
                  load={(q) => api.trackerAnalytics(stats.id, q)}
                  kind={stats.kind}
                  intro={<Mono>{stats.url}</Mono>}
                />
              </div>
            </>
          )}
        </SheetContent>
      </Sheet>
    </Section>
  );
}

/** Splits a link's target into its URL and the UTM tags the dialog edits apart. */
function splitTarget(target: string) {
  try {
    const u = new URL(target);
    const utm = Object.fromEntries(utmKeys.map((k) => [k, u.searchParams.get(k) ?? ""]));
    for (const k of utmKeys) u.searchParams.delete(k);
    return { url: u.toString(), utm };
  } catch {
    return { url: target, utm: Object.fromEntries(utmKeys.map((k) => [k, ""])) };
  }
}

function joinTarget(url: string, utm: Record<string, string>) {
  try {
    const u = new URL(url.trim());
    for (const k of utmKeys) if (utm[k].trim()) u.searchParams.set(k, utm[k].trim());
    return u.toString();
  } catch {
    return url.trim(); // the server says what's wrong
  }
}

function TrackerDialog({ initial, onClose }: { initial: Tracker | TrackerKind; onClose: () => void }) {
  const qc = useQueryClient();
  const existing = typeof initial === "string" ? null : initial;
  const kind = existing?.kind ?? (initial as TrackerKind);
  const split = splitTarget(existing?.target ?? "");
  const [name, setName] = useState(existing?.name ?? "");
  const [slug, setSlug] = useState(existing?.slug ?? "");
  const [url, setUrl] = useState(existing ? split.url : "");
  const [utm, setUtm] = useState<Record<string, string>>(split.utm);
  const save = useMutation({
    meta: { error: "Couldn't save it" },
    mutationFn: () => {
      const t = { kind, name, slug, target: kind === "link" ? joinTarget(url, utm) : undefined };
      return existing ? api.updateTracker(existing.id, t) : api.createTracker(t);
    },
    onSuccess: (t) => {
      qc.invalidateQueries({ queryKey: ["trackers"] });
      toast.success(existing ? `${t.name} saved` : `${t.name} created`, { description: t.url });
      onClose();
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={onSubmit} className="contents">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2 [&>svg]:size-5">
              {kind === "link" ? <Link2 /> : <Image />}
              {existing ? `Edit ${existing.name}` : kind === "link" ? "New link" : "New pixel"}
            </DialogTitle>
            <DialogDescription>
              {kind === "link"
                ? "Share its URL instead of the target's: each visit redirects and counts a click, with where it came from."
                : "Put its image where views should count: an email, a README, a page without the script."}
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 sm:grid-cols-2">
            <FloatingInput label="Name" required maxLength={100} value={name} onChange={(e) => setName(e.target.value)} className="sm:col-span-2" autoFocus />
            {kind === "link" && (
              <>
                <FloatingInput
                  label="Target URL"
                  type="url"
                  required
                  value={url}
                  onChange={(e) => setUrl(e.target.value)}
                  className="sm:col-span-2"
                  placeholder="https://example.com/launch"
                />
                <div className="grid gap-4 sm:col-span-2 sm:grid-cols-3">
                  {utmKeys.map((k) => (
                    <FloatingInput key={k} label={`UTM ${k.slice(4)}`} value={utm[k]} onChange={(e) => setUtm({ ...utm, [k]: e.target.value })} />
                  ))}
                  <p className="-mt-2 text-xs text-muted-foreground sm:col-span-3">Optional, added to the target.</p>
                </div>
              </>
            )}
            <FloatingInput
              label="Slug"
              value={slug}
              onChange={(e) => setSlug(e.target.value.toLowerCase())}
              pattern="[a-z0-9][a-z0-9\-]{0,62}"
              description={existing ? "Changing it breaks the old URL" : "Random when empty"}
              className="sm:col-span-2"
            />
          </div>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" loading={save.isPending}>
              {existing ? "Save" : "Create"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
