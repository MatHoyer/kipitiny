import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { Connection, Edge, IsValidConnection } from "@xyflow/react";
import { useRef, useState, type FormEvent } from "react";
import { toast } from "sonner";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { Mono } from "@/components/common";
import { dbRef } from "@/lib/format";
import { api, isDatabase, type ServerTopology, type TopoProject, type TopoService } from "@/api";

/*
 * What the canvas can change by drawing and deleting lines:
 *   service → network created by hand: the service joins it (live, no restart);
 *   app → database of its project: an env variable references it (next deploy).
 * Deleting such a line undoes it. Other lines (routing, tunnel) are read-only.
 */

type Found = { svc: TopoService; project: TopoProject; server: ServerTopology };

const envRefRe = (db: string) => new RegExp(`\\{\\{\\s*db\\.${db.replace(/[-]/g, "\\-")}\\.`);

export function useCanvasEdits(servers: ServerTopology[]) {
  const qc = useQueryClient();
  const [link, setLink] = useState<{ app: Found; db: Found } | null>(null);
  const [removal, setRemoval] = useState<{ edges: Edge[]; lines: string[] } | null>(null);
  const resolveRemoval = useRef<(ok: boolean) => void>(undefined);

  const find = (nodeId: string | null): Found | undefined => {
    const id = nodeId?.startsWith("svc:") ? nodeId.slice(4) : undefined;
    if (!id) return undefined;
    for (const server of servers)
      for (const project of server.projects) {
        const svc = project.services.find((s) => s.id === id);
        if (svc) return { svc, project, server };
      }
  };
  const network = (nodeId: string | null) => {
    const id = nodeId?.startsWith("net:") ? nodeId.slice(4) : undefined;
    if (!id) return undefined;
    for (const server of servers) {
      const net = server.networks.find((n) => n.customId === id);
      if (net) return { net, server };
    }
  };
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["topology"] });
    qc.invalidateQueries({ queryKey: ["networks"] });
    qc.invalidateQueries({ queryKey: ["service"] });
  };

  const setNetworks = useMutation({
    meta: { error: "Couldn't change the service's networks" },
    mutationFn: ({ svc, networks }: { svc: TopoService; networks: string[] }) => api.setServiceNetworks(svc.id, networks),
    onSettled: refresh,
  });

  /** Why a line from source to target can't be drawn, or "". */
  const refusal = (c: Connection | Edge): string => {
    const from = find(c.source);
    if (!from) return "Lines start at a service";
    const net = network(c.target);
    if (net) {
      if (net.server.id !== from.server.id) return "The network is on another server";
      if (from.svc.hostNetwork) return "An app in the host network can't join other networks";
      if (from.svc.networks.includes(net.net.customId!)) return "Already on this network";
      return "";
    }
    const to = find(c.target);
    if (!to || !isDatabase(to.svc.kind)) return "Draw lines to a database or a network";
    if (isDatabase(from.svc.kind)) return "Only apps use databases";
    if (to.project.id !== from.project.id) return "Apps only reference databases of their own project; put both on a network instead";
    if (from.svc.uses.includes(to.svc.id)) return "It already uses this database";
    return "";
  };


  const isValidConnection: IsValidConnection = (c) => refusal(c) === "";

  const onConnect = (c: Connection) => {
    const why = refusal(c);
    if (why) return void toast.error(why);
    const from = find(c.source)!;
    const net = network(c.target);
    if (net) {
      setNetworks.mutate(
        { svc: from.svc, networks: [...from.svc.networks, net.net.customId!] },
        { onSuccess: () => toast.success(`${from.svc.name} joined ${net.net.custom}`, { description: `Reached there as ${from.project.name}-${from.svc.name}.` }) },
      );
      return;
    }
    setLink({ app: from, db: find(c.target)! });
  };

  /** Describes what deleting the lines does, and asks first. */
  const onBeforeDelete = async ({ nodes, edges }: { nodes: unknown[]; edges: Edge[] }) => {
    if (nodes.length > 0) return false;
    const editable = edges.filter((e) => e.data?.kind === "net" || e.data?.kind === "db");
    if (editable.length === 0) return false;
    const lines = editable.map((e) => {
      const from = find(e.source);
      if (e.data?.kind === "net") return `${from?.svc.name} leaves ${network(e.target)?.net.custom}`;
      return `${from?.svc.name} stops referencing ${find(e.target)?.svc.name} (its variables are removed; applies on its next deploy)`;
    });
    const ok = await new Promise<boolean>((resolve) => {
      resolveRemoval.current = resolve;
      setRemoval({ edges: editable, lines });
    });
    setRemoval(null);
    if (ok) await removeEdges(editable);
    return false; // the next topology refresh drops the lines
  };

  const removeEdges = async (edges: Edge[]) => {
    try {
      for (const e of edges) {
        const from = find(e.source);
        if (!from) continue;
        if (e.data?.kind === "net") {
          const id = e.target.slice(4);
          await api.setServiceNetworks(from.svc.id, from.svc.networks.filter((n) => n !== id));
        } else {
          const db = find(e.target);
          if (!db) continue;
          const svc = await api.service(from.svc.id);
          const env = Object.fromEntries(Object.entries(svc.env).filter(([, v]) => !envRefRe(db.svc.name).test(v)));
          if (Object.keys(env).length === Object.keys(svc.env).length) {
            // The reference sits inside a secret, which reads back masked.
            toast.error(`${svc.name}'s reference to ${db.svc.name} is inside a secret`, { description: "Edit it in the service's Environment tab." });
            continue;
          }
          await api.updateService(svc.id, { env, secrets: svc.secrets.filter((k) => k in env) });
        }
      }
    } catch (err) {
      toast.error("Couldn't remove the line", { description: err instanceof Error ? err.message : String(err) });
    }
    refresh();
  };

  const dialogs = (
    <>
      <AlertDialog
        open={removal !== null}
        onOpenChange={(open) => {
          if (!open) resolveRemoval.current?.(false);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove {removal?.edges.length === 1 ? "this line" : `${removal?.edges.length} lines`}?</AlertDialogTitle>
            <AlertDialogDescription asChild>
              <ul className="list-disc space-y-1 pl-5">
                {removal?.lines.map((l) => (
                  <li key={l}>{l}</li>
                ))}
              </ul>
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <Button variant="destructive" onClick={() => resolveRemoval.current?.(true)}>
              Remove
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      <Dialog open={link !== null} onOpenChange={(open) => !open && setLink(null)}>
        <DialogContent>{link && <ReferenceForm app={link.app.svc} db={link.db.svc} onDone={() => (setLink(null), refresh())} />}</DialogContent>
      </Dialog>
    </>
  );

  return { isValidConnection, onConnect, onBeforeDelete, dialogs };
}

/** Adds an env variable referencing a database to an app, as a secret. */
function ReferenceForm({ app, db, onDone }: { app: TopoService; db: TopoService; onDone: () => void }) {
  const [key, setKey] = useState(db.kind === "redis" ? "REDIS_URL" : "DATABASE_URL");
  const value = dbRef(db.name, "URL");
  const save = useMutation({
    meta: { error: "Couldn't add the variable" },
    mutationFn: async () => {
      const svc = await api.service(app.id);
      if (key in svc.env) throw new Error(`${app.name} already has ${key}; pick another name.`);
      return api.updateService(app.id, { env: { ...svc.env, [key]: value }, secrets: [...svc.secrets, key] });
    },
    onSuccess: () => {
      toast.success(`${app.name} now uses ${db.name}`, { description: "Deploy it to apply." });
      onDone();
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };
  return (
    <form onSubmit={onSubmit} className="contents">
      <DialogHeader>
        <DialogTitle>
          Use {db.name} in {app.name}
        </DialogTitle>
        <DialogDescription>
          Adds a secret variable set to <Mono>{value}</Mono>, resolved at each deploy. Deploy {app.name} to apply it.
        </DialogDescription>
      </DialogHeader>
      <FloatingInput label="Variable" required autoFocus value={key} onChange={(e) => setKey(e.target.value.toUpperCase())} inputClassName="font-mono" />
      <DialogFooter>
        <Button type="submit" loading={save.isPending}>
          Add variable
        </Button>
      </DialogFooter>
    </form>
  );
}
