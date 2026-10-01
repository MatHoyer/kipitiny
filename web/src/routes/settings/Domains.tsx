import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Globe, Plus, Trash2, TriangleAlert } from "lucide-react";
import { useState, type FormEvent } from "react";
import { CloudflareIcon } from "@/components/brand-icons";
import { CheckboxField, EmptyState, ErrorText, Mono, Tag } from "@/components/common";
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
  DialogTrigger,
} from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { timeAgo } from "@/lib/format";
import { api, type Domain } from "@/api";
import { SettingsPage } from "./page";

const cloudflareIcon = <CloudflareIcon className="text-[#F38020]" />;

/** Base domains for service addresses, and the Cloudflare token that keeps their DNS in sync. */
export function DomainsPage() {
  const domains = useQuery({ queryKey: ["domains"], queryFn: api.domains });

  return (
    <SettingsPage actions={!!domains.data?.length && <AddDomainDialog />}>
      <CloudflareCard />
      <ErrorText error={domains.error} />
      {domains.data?.length === 0 ? (
        <EmptyState
          icon={Globe}
          title="No domains yet"
          description="Add a domain you own to give services public addresses on it."
          action={<AddDomainDialog />}
        />
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {domains.data?.map((d) => (
            <DomainCard key={d.id} domain={d} />
          ))}
        </div>
      )}
    </SettingsPage>
  );
}

function DomainCard({ domain: d }: { domain: Domain }) {
  const qc = useQueryClient();
  const refresh = () => qc.invalidateQueries({ queryKey: ["domains"] });
  const remove = useMutation({ meta: { error: "Couldn't remove the domain" }, mutationFn: api.deleteDomain, onSuccess: refresh });
  const proxy = useMutation({
    meta: { error: "Couldn't change the Cloudflare proxy" },
    mutationFn: (proxied: boolean) => api.setDomainProxied(d.id, proxied),
    onSuccess: refresh,
  });
  const loops = d.cloudflare && d.proxied && (d.sslMode === "flexible" || d.sslMode === "off");

  return (
    <Card className="gap-3 px-4">
      <div className="flex items-start gap-3">
        <span className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-muted [&>svg]:size-5">
          {d.cloudflare ? cloudflareIcon : <Globe />}
        </span>
        <div className="min-w-0 flex-1">
          <p className="truncate font-mono text-sm font-medium">{d.name}</p>
          <p className="truncate text-xs text-muted-foreground">
            {d.cloudflare ? "DNS managed in Cloudflare" : "DNS records are up to you"}
          </p>
        </div>
        {d.cloudflare && d.proxied && <Tag>proxied</Tag>}
      </div>
      {d.cloudflare && (
        <CheckboxField
          label="Proxied"
          description="Through Cloudflare's proxy (orange cloud): protection, and the server's IP stays hidden."
          checked={d.proxied}
          onCheckedChange={(proxied) => proxy.mutate(proxied)}
        />
      )}
      {loops && (
        <p className="flex gap-1.5 text-xs text-destructive">
          <TriangleAlert className="mt-px size-3.5 shrink-0" />
          <span>
            This zone's SSL/TLS mode is <Mono>{d.sslMode}</Mono>: set it to Full (strict) in Cloudflare, or proxied sites loop
            between HTTP and HTTPS.
          </span>
        </p>
      )}
      <div className="mt-auto flex items-center justify-between gap-2">
        <p className="text-xs text-muted-foreground">Added {timeAgo(d.createdAt)}</p>
        <ConfirmDialog
          trigger={
            <Button variant="ghost" size="icon-sm" title="Remove" aria-label="Remove" className="text-muted-foreground hover:text-destructive">
              <Trash2 />
            </Button>
          }
          title={`Remove ${d.name}?`}
          description="It's only removed from this list: services using it keep their domain."
          confirmLabel="Remove"
          onConfirm={() => remove.mutate(d.id)}
        />
      </div>
    </Card>
  );
}

function AddDomainDialog() {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const onOpenChange = (next: boolean) => {
    setOpen(next);
    if (!next) {
      setName("");
      create.reset();
    }
  };
  const create = useMutation({
    meta: { error: "Couldn't add the domain" },
    mutationFn: () => api.createDomain(name.trim()),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["domains"] });
      onOpenChange(false);
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    create.mutate();
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button size="sm">
          <Plus data-icon="inline-start" />
          Add domain
        </Button>
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={onSubmit} className="contents">
          <DialogHeader>
            <DialogTitle>Add a domain</DialogTitle>
            <DialogDescription>
              Services can then take addresses on it, like <Mono>app.{name.trim() || "example.com"}</Mono>.
            </DialogDescription>
          </DialogHeader>
          <FloatingInput
            label="Domain"
            required
            autoFocus
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="example.com"
            description="In a zone of the connected Cloudflare token, its DNS records are created for you."
          />
          <DialogFooter>
            <Button type="submit" loading={create.isPending}>
              Add
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/** Lets the manager keep DNS records (and tunnel routes) in sync. */
function CloudflareCard() {
  const qc = useQueryClient();
  const cf = useQuery({ queryKey: ["cloudflare"], queryFn: api.cloudflare, refetchInterval: 30_000 });
  const disconnect = useMutation({
    meta: { error: "Couldn't disconnect Cloudflare" },
    mutationFn: api.disconnectCloudflare,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["cloudflare"] });
      qc.invalidateQueries({ queryKey: ["domains"] });
    },
  });
  const status = cf.data;
  if (!status) return <ErrorText error={cf.error} />;

  return (
    <Card className="gap-3 px-4">
      <div className="flex flex-wrap items-start gap-3">
        <span className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-muted [&>svg]:size-5">{cloudflareIcon}</span>
        <div className="min-w-0 flex-1 space-y-0.5">
          <p className="font-medium">Cloudflare</p>
          <p className="text-xs text-muted-foreground">
            {status.connected
              ? `Connected · ${status.zones.length} zone${status.zones.length === 1 ? "" : "s"}${status.syncedAt ? ` · synced ${timeAgo(status.syncedAt)}` : ""}`
              : "Not connected: services' DNS records are up to you."}
          </p>
        </div>
        {status.connected ? (
          <ConfirmDialog
            trigger={
              <Button variant="outline" size="sm" loading={disconnect.isPending}>
                Disconnect
              </Button>
            }
            title="Disconnect Cloudflare?"
            description="kipitiny stops managing DNS. Records it created stay in Cloudflare."
            confirmLabel="Disconnect"
            onConfirm={() => disconnect.mutate()}
          />
        ) : (
          <ConnectCloudflareDialog />
        )}
      </div>
      {status.connected && status.zones.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {status.zones.map((z) => (
            <Tag key={z} className="font-mono">
              {z}
            </Tag>
          ))}
        </div>
      )}
      {status.connected && status.tunnels.length > 0 && (
        <p className="text-xs text-muted-foreground">Tunnel routes managed for {status.tunnels.map((t) => t.server).join(", ")}.</p>
      )}
      {status.error && <p className="text-sm text-destructive">{status.error}</p>}
      {status.tunnelError && <p className="text-sm text-destructive">{status.tunnelError}</p>}
    </Card>
  );
}

function ConnectCloudflareDialog() {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [token, setToken] = useState("");
  const onOpenChange = (next: boolean) => {
    setOpen(next);
    if (!next) {
      setToken("");
      connect.reset();
    }
  };
  const connect = useMutation({
    meta: { error: "Couldn't connect Cloudflare" },
    mutationFn: () => api.connectCloudflare(token.trim()),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["cloudflare"] });
      qc.invalidateQueries({ queryKey: ["domains"] });
      onOpenChange(false);
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    connect.mutate();
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button size="sm">Connect</Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={onSubmit} className="contents">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2 [&>svg]:size-5">
              {cloudflareIcon}
              Connect Cloudflare
            </DialogTitle>
            <DialogDescription>
              kipitiny creates the DNS record of every service domain in your Cloudflare zones (an A record to the server, or a
              CNAME to your tunnel along with its route) and removes it when the domain goes away. Records it didn't create are
              never changed.
            </DialogDescription>
          </DialogHeader>
          <FloatingInput
            label="API token"
            type="password"
            required
            autoFocus
            autoComplete="off"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            description={
              <>
                Permissions: <Mono>Zone › Zone › Read</Mono>, <Mono>Zone › DNS › Edit</Mono>, and{" "}
                <Mono>Account › Cloudflare Tunnel › Edit</Mono> with a tunnel.
              </>
            }
          />
          <DialogFooter>
            <Button type="submit" loading={connect.isPending}>
              Connect
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
