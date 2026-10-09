import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Network as NetworkIcon, Plus, Trash2 } from "lucide-react";
import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { EmptyState, ErrorText, Mono } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { ServiceIcon } from "@/components/service-icon";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingSelect } from "@/components/ui/floating-select";
import { api, type Network } from "@/api";
import { SettingsPage } from "./page";

/** Networks created by hand, which services of several projects join to reach each other. */
export function Networks() {
  const qc = useQueryClient();
  const networks = useQuery({ queryKey: ["networks"], queryFn: api.networks });
  const servers = useQuery({ queryKey: ["servers"], queryFn: api.servers });
  const [creating, setCreating] = useState(false);
  const multi = (servers.data?.length ?? 0) > 1;
  const serverName = (id: string) => servers.data?.find((s) => s.id === id)?.name ?? id;
  const remove = useMutation({
    meta: { error: "Couldn't delete the network" },
    mutationFn: api.deleteNetwork,
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ["networks"] });
      qc.invalidateQueries({ queryKey: ["service"] });
    },
  });
  const add = (
    <Button size="sm" onClick={() => setCreating(true)}>
      <Plus data-icon="inline-start" />
      Create network
    </Button>
  );

  return (
    <SettingsPage actions={!!networks.data?.length && add}>
      <p className="mb-4 max-w-3xl text-sm text-muted-foreground">
        Each project has its own private network, and public apps share the proxy network with Traefik: those are automatic. Create a
        network here to let services of different projects reach each other; a service joins it from its Settings tab and answers there as{" "}
        <Mono>project-service</Mono>.
      </p>
      <ErrorText error={networks.error} />
      {networks.data?.length === 0 ? (
        <EmptyState
          icon={NetworkIcon}
          title="No networks created by hand"
          description="Services only reach the others of their own project. Create a network to connect services across projects, e.g. several apps sharing one database."
          action={add}
        />
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {networks.data?.map((n) => (
            <NetworkCard key={n.id} network={n} server={multi ? serverName(n.serverId) : undefined} onDelete={() => remove.mutate(n.id)} />
          ))}
        </div>
      )}
      <Dialog open={creating} onOpenChange={setCreating}>
        <DialogContent>{creating && <CreateNetworkForm onDone={() => setCreating(false)} />}</DialogContent>
      </Dialog>
    </SettingsPage>
  );
}

function NetworkCard({ network: n, server, onDelete }: { network: Network; server?: string; onDelete: () => void }) {
  return (
    <Card className="gap-3 px-4">
      <div className="flex items-start gap-3">
        <span className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-muted [&>svg]:size-5">
          <NetworkIcon />
        </span>
        <div className="min-w-0 flex-1">
          <p className="truncate font-medium">{n.name}</p>
          <p className="truncate font-mono text-xs text-muted-foreground" title={n.dockerName}>
            {server ? `${server} · ` : ""}
            {n.dockerName}
          </p>
        </div>
      </div>
      {n.services.length === 0 ? (
        <p className="text-sm text-muted-foreground">No services yet: add them from a service's Settings tab.</p>
      ) : (
        <ul className="space-y-1">
          {n.services.map((m) => (
            <li key={m.id} className="flex min-w-0 items-center gap-2 text-sm">
              <ServiceIcon service={{ kind: m.kind }} className="size-4 shrink-0 text-muted-foreground" />
              <Link to={`/services/${m.id}`} className="truncate hover:underline">
                {m.projectName}/{m.name}
              </Link>
              <span className="ml-auto shrink-0 font-mono text-xs text-muted-foreground" title="Name on this network">
                {m.alias}
              </span>
            </li>
          ))}
        </ul>
      )}
      <div className="mt-auto flex justify-end">
        <ConfirmDialog
          trigger={
            <Button variant="ghost" size="icon-sm" title="Delete" aria-label={`Delete ${n.name}`} className="text-muted-foreground hover:text-destructive">
              <Trash2 />
            </Button>
          }
          title={`Delete network ${n.name}?`}
          description={
            n.services.length > 0
              ? `Its ${n.services.length} service${n.services.length > 1 ? "s" : ""} leave it right away and can no longer reach each other through it. Nothing restarts.`
              : "It has no services."
          }
          confirmLabel="Delete"
          onConfirm={onDelete}
        />
      </div>
    </Card>
  );
}

/** Creates a network; a dialog's content. */
export function CreateNetworkForm({ onDone }: { onDone: () => void }) {
  const qc = useQueryClient();
  const servers = useQuery({ queryKey: ["servers"], queryFn: api.servers });
  const [name, setName] = useState("");
  const [serverId, setServerId] = useState("local");
  const create = useMutation({
    meta: { error: "Couldn't create the network" },
    mutationFn: () => api.createNetwork({ name: name.trim(), serverId }),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ["networks"] });
      qc.invalidateQueries({ queryKey: ["topology"] });
    },
    onSuccess: onDone,
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (name.trim()) create.mutate();
  };

  return (
    <form onSubmit={onSubmit} className="contents">
      <DialogHeader>
        <DialogTitle>Create a network</DialogTitle>
        <DialogDescription>Services of any project on its server can join it. Databases on it are still never public.</DialogDescription>
      </DialogHeader>
      <div className="space-y-4">
        <FloatingInput
          label="Name"
          required
          autoFocus
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="shared"
          description="Lowercase letters, digits and dashes."
        />
        {(servers.data?.length ?? 0) > 1 && (
          <FloatingSelect
            label="Server"
            value={serverId}
            onValueChange={setServerId}
            options={servers.data!.map((s) => ({ value: s.id, label: s.name }))}
            description="Only services on this server can join it."
          />
        )}
      </div>
      <DialogFooter>
        <Button type="submit" loading={create.isPending}>
          Create
        </Button>
      </DialogFooter>
    </form>
  );
}
