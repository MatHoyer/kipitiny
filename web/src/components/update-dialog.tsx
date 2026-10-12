import { useQuery } from "@tanstack/react-query";
import { CircleCheck, CircleX, TriangleAlert } from "lucide-react";
import { useState, type ReactNode } from "react";
import { Link } from "react-router";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";
import { cn } from "@/lib/utils";
import { api, type UpdatePreflight } from "@/api";

const titles: Record<UpdatePreflight["checks"][number]["id"], string> = {
  version: "New version",
  tasks: "Nothing in progress",
  docker: "Docker",
  backup: "A recent manager backup",
};

const marks = {
  ok: <CircleCheck className="size-4 text-emerald-600" />,
  warn: <TriangleAlert className="size-4 text-amber-500" />,
  block: <CircleX className="size-4 text-destructive" />,
};

/**
 * Updating the manager, after checking now is a good time: nothing in progress that its restart would cut short,
 * Docker answering, and a recent backup of it. The checks refresh while it's open, so a deploy finishing unblocks it.
 */
export function UpdateDialog({ trigger, latest, onUpdate }: { trigger: ReactNode; latest: string; onUpdate: () => void }) {
  const [open, setOpen] = useState(false);
  const preflight = useQuery({ queryKey: ["update-preflight"], queryFn: api.updatePreflight, enabled: open, refetchInterval: 2_000 });
  const p = preflight.data;
  const backupWarn = p?.checks.some((c) => c.id === "backup" && c.state === "warn");

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Update to {latest}?</DialogTitle>
          <DialogDescription>
            The manager restarts on the new version. Apps, databases and Traefik keep running. If the new version doesn't start, the previous one
            is restored.
          </DialogDescription>
        </DialogHeader>
        <ul className="grid gap-3">
          {p
            ? p.checks.map((c) => (
                <li key={c.id} className="flex items-start gap-3">
                  <span className="mt-0.5 shrink-0 animate-in zoom-in-50">{marks[c.state]}</span>
                  <div className="min-w-0 text-sm">
                    <p className="font-medium">{titles[c.id]}</p>
                    <p className={cn("text-xs", c.state === "block" ? "text-destructive" : "text-muted-foreground")}>{c.detail}</p>
                  </div>
                </li>
              ))
            : Object.values(titles).map((t) => (
                <li key={t} className="flex items-center gap-3 text-sm text-muted-foreground">
                  <Spinner />
                  {t}
                </li>
              ))}
        </ul>
        {p && !p.ready && <p className="text-xs text-muted-foreground">Checked again every few seconds: it unblocks once what's in progress ends.</p>}
        <DialogFooter>
          {backupWarn && (
            <Button variant="ghost" asChild className="sm:mr-auto">
              <Link to="/backups?tab=manager" onClick={() => setOpen(false)}>
                Back up first
              </Link>
            </Button>
          )}
          <Button
            disabled={!p?.ready}
            onClick={() => {
              setOpen(false);
              onUpdate();
            }}
          >
            {p && !p.ready ? "Not now" : "Update now"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
