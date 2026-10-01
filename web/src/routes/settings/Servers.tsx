import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Globe, HardDrive, Pencil, Plus, Server as ServerIcon, Trash2 } from "lucide-react";
import { useState, type FormEvent } from "react";
import { CheckboxField, CopyField, IconTile, Mono, Section, StateBadge, Tag } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { api, SECRET_MASK, type Server, type ServerInput } from "@/api";

export function Servers() {
  const qc = useQueryClient();
  const servers = useQuery({ queryKey: ["servers"], queryFn: api.servers, refetchInterval: 30_000 });
  const [editing, setEditing] = useState<Server | "new" | null>(null);
  const [ipFor, setIpFor] = useState<Server | null>(null);
  const remove = useMutation({
    meta: { error: "Couldn't remove the server" },
    mutationFn: api.deleteServer,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["servers"] }),
  });

  return (
    <Section
      title="Servers"
      description="Docker hosts projects run on. Remote ones are reached over SSH."
      actions={
        <Button variant="outline" size="sm" onClick={() => setEditing("new")}>
          <Plus data-icon="inline-start" />
          Add server
        </Button>
      }
    >
      <div className="grid gap-3 lg:grid-cols-2">
        {servers.data?.map((s) => (
          <Card key={s.id} size="sm" className="px-3">
            <div className="flex items-start gap-3">
              <IconTile icon={s.kind === "local" ? HardDrive : ServerIcon} />
              <div className="min-w-0 flex-1">
                <p className="flex items-center gap-2 font-medium">
                  <span className="truncate">{s.name}</span>
                  <StateBadge state={s.docker ? "running" : "failed"} />
                </p>
                <p className="truncate font-mono text-xs text-muted-foreground">
                  {s.kind === "local" ? "the manager's own Docker" : `ssh://${s.sshUser}@${s.host}:${s.port}`}
                </p>
              </div>
              <div className="-mt-1 -mr-1 flex">
                <Button variant="ghost" size="icon-sm" title="Network" aria-label="Network" onClick={() => setIpFor(s)}>
                  <Globe />
                </Button>
                {s.kind === "ssh" && (
                  <Button variant="ghost" size="icon-sm" title="Edit" aria-label="Edit" onClick={() => setEditing(s)}>
                    <Pencil />
                  </Button>
                )}
                {s.kind === "ssh" && (
                  <ConfirmDialog
                    trigger={
                      <Button variant="ghost" size="icon-sm" title="Remove" aria-label="Remove" className="text-muted-foreground hover:text-destructive">
                        <Trash2 />
                      </Button>
                    }
                    title={`Remove server ${s.name}?`}
                    confirmLabel="Remove"
                    onConfirm={() => remove.mutate(s.id)}
                  />
                )}
              </div>
            </div>
            <div className="flex flex-wrap gap-1.5">
              <Tag>{s.docker ? `Docker ${s.docker.version} · ${s.docker.arch}` : "Docker unreachable"}</Tag>
              <Tag>{`${s.projects} project${s.projects === 1 ? "" : "s"}`}</Tag>
              {s.tunnel ? (
                <Tag>Cloudflare tunnel</Tag>
              ) : s.publicIp ? (
                <Tag>IP {s.publicIp}</Tag>
              ) : (
                s.detectedIp && <Tag>IP {s.detectedIp} (detected)</Tag>
              )}
            </div>
            {s.dockerError && <p className="text-xs text-destructive">{s.dockerError}</p>}
          </Card>
        ))}
      </div>
      <Dialog open={ipFor !== null} onOpenChange={(o) => !o && setIpFor(null)}>
        <DialogContent className="sm:max-w-md">
          {ipFor && <NetworkForm key={ipFor.id} server={ipFor} onDone={() => setIpFor(null)} />}
        </DialogContent>
      </Dialog>
      <Dialog open={editing !== null} onOpenChange={(o) => !o && setEditing(null)}>
        <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-lg">
          {editing && (
            <ServerForm
              key={editing === "new" ? "new" : editing.id}
              server={editing === "new" ? null : editing}
              onDone={() => setEditing(null)}
            />
          )}
        </DialogContent>
      </Dialog>
    </Section>
  );
}

function ServerForm({ server, onDone }: { server: Server | null; onDone: () => void }) {
  const qc = useQueryClient();
  const key = useQuery({ queryKey: ["ssh-key"], queryFn: api.sshKey });
  const [form, setForm] = useState({
    name: server?.name ?? "",
    host: server?.host ?? "",
    port: String(server?.port ?? 22),
    sshUser: server?.sshUser ?? "root",
    socket: server?.socket ?? "/var/run/docker.sock",
  });
  const [resetHostKey, setResetHostKey] = useState(false);
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });
  const save = useMutation({
    meta: { error: "Couldn't save the server" },
    mutationFn: () => {
      const input: ServerInput = { ...form, port: Number(form.port) || 22, resetHostKey };
      return server ? api.updateServer(server.id, input) : api.createServer(input);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["servers"] });
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
        <DialogTitle>{server ? `Edit ${server.name}` : "New server"}</DialogTitle>
        <DialogDescription>
          On the server, add the manager's key to <Mono>~/.ssh/authorized_keys</Mono> of the SSH user (who needs access
          to the Docker socket).
        </DialogDescription>
      </DialogHeader>
      <CopyField value={key.data?.publicKey ?? "…"} />
      <p className="text-xs text-muted-foreground">
        sshd must allow <Mono>AllowTcpForwarding yes</Mono> (or <Mono>local</Mono>) and{" "}
        <Mono>AllowStreamLocalForwarding yes</Mono>: the Docker API is reached through the socket, never over TCP. The
        server's host key is pinned on first connection.
      </p>
      <div className="grid gap-4 sm:grid-cols-2">
        <FloatingInput label="Name" required autoFocus value={form.name} onChange={set("name")} placeholder="db-server-2" />
        <FloatingInput label="Host" required value={form.host} onChange={set("host")} placeholder="203.0.113.10" />
        <FloatingInput label="SSH port" type="number" min={1} max={65535} value={form.port} onChange={set("port")} />
        <FloatingInput label="SSH user" required value={form.sshUser} onChange={set("sshUser")} />
        <FloatingInput
          label="Docker socket"
          required
          value={form.socket}
          onChange={set("socket")}
          inputClassName="font-mono"
          className="sm:col-span-2"
        />
        {server?.hostKey && (
          <CheckboxField
            label="Forget the pinned host key"
            description="Only after reinstalling the server."
            checked={resetHostKey}
            onCheckedChange={setResetHostKey}
            className="sm:col-span-2"
          />
        )}
      </div>
      <DialogFooter>
        <Button type="submit" disabled={save.isPending}>
          {save.isPending ? "Connecting…" : server ? "Save" : "Add server"}
        </Button>
      </DialogFooter>
    </form>
  );
}

/** How a server's apps are reached: its public IP, or a Cloudflare tunnel. */
function NetworkForm({ server, onDone }: { server: Server; onDone: () => void }) {
  const qc = useQueryClient();
  const [ip, setIp] = useState(server.publicIp);
  const [token, setToken] = useState(server.tunnel === "server" ? SECRET_MASK : "");
  const save = useMutation({
    meta: { error: "Couldn't save the network settings" },
    mutationFn: () => api.setServerNetwork(server.id, { publicIp: ip.trim(), tunnelToken: token.trim() }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["servers"] });
      qc.invalidateQueries({ queryKey: ["cloudflare"] });
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
        <DialogTitle>Network of {server.name}</DialogTitle>
        <DialogDescription>How its apps are reached from the internet.</DialogDescription>
      </DialogHeader>
      <div className="space-y-4">
        <FloatingInput
          label="Cloudflare tunnel token"
          type="password"
          autoComplete="off"
          value={token}
          onChange={(e) => setToken(e.target.value)}
          description={
            server.tunnel === "env" && !token ? (
              <>
                Set by <Mono>KIPITINY_CLOUDFLARE_TUNNEL_TOKEN</Mono>; a token here replaces it.
              </>
            ) : (
              "Its own tunnel (one per server). With a token, cloudflared runs there and ports 80/443 aren't published. Empty for public ports."
            )
          }
        />
        <FloatingInput
          label="Public IPv4"
          value={ip}
          onChange={(e) => setIp(e.target.value)}
          placeholder={server.detectedIp || "203.0.113.10"}
          description={
            <>
              Where Cloudflare DNS records point without a tunnel. Empty to detect it
              {server.detectedIp && (
                <>
                  {" "}
                  (currently <Mono>{server.detectedIp}</Mono>)
                </>
              )}
              .
            </>
          }
        />
      </div>
      <DialogFooter>
        <Button type="submit" disabled={save.isPending}>
          Save
        </Button>
      </DialogFooter>
    </form>
  );
}
