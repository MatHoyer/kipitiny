import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Trash2 } from "lucide-react";
import { useState, type FormEvent } from "react";
import { CheckboxField, Empty, Mono, Section, Tag } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { FloatingInput } from "@/components/ui/floating-input";
import { timeAgo } from "@/lib/format";
import { api } from "@/api";

export function DomainsPage() {
  return (
    <>
      <CloudflareSection />
      <Domains />
    </>
  );
}

function Domains() {
  const qc = useQueryClient();
  const domains = useQuery({ queryKey: ["domains"], queryFn: api.domains });
  const [name, setName] = useState("");
  const refresh = () => qc.invalidateQueries({ queryKey: ["domains"] });
  const create = useMutation({
    meta: { error: "Couldn't add the domain" },
    mutationFn: () => api.createDomain(name),
    onSuccess: () => {
      setName("");
      refresh();
    },
  });
  const remove = useMutation({ meta: { error: "Couldn't remove the domain" }, mutationFn: api.deleteDomain, onSuccess: refresh });
  const proxy = useMutation({
    meta: { error: "Couldn't change the Cloudflare proxy" },
    mutationFn: ({ id, proxied }: { id: string; proxied: boolean }) => api.setDomainProxied(id, proxied),
    onSuccess: refresh,
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    create.mutate();
  };

  return (
    <Section
      title="Domains"
      description={
        <>
          Domains you own, offered when giving a service a public address (<Mono>shop.example.com</Mono>). Point their DNS
          at this server, or add them to your Cloudflare tunnel.
        </>
      }
    >
      <form onSubmit={onSubmit} className="flex items-start gap-2">
        <FloatingInput
          label="Domain"
          required
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="example.com"
          className="flex-1"
        />
        <Button type="submit" className="h-14" disabled={create.isPending}>
          <Plus data-icon="inline-start" />
          Add
        </Button>
      </form>
      {domains.data?.length === 0 ? (
        <Empty>No domains yet.</Empty>
      ) : (
        <ul className="mt-2 divide-y">
          {domains.data?.map((d) => (
            <li key={d.id} className="flex items-center justify-between gap-4 py-2">
              <div className="min-w-0 space-y-1">
                <p className="flex flex-wrap items-center gap-2">
                  <span className="font-mono text-sm">{d.name}</span>
                  {d.cloudflare && <Tag>Cloudflare DNS</Tag>}
                </p>
                {d.cloudflare && (
                  <CheckboxField
                    label="Proxied"
                    description="Through Cloudflare's proxy (orange cloud): protection, and the server's IP stays hidden."
                    checked={d.proxied}
                    onCheckedChange={(proxied) => proxy.mutate({ id: d.id, proxied })}
                  />
                )}
                {d.cloudflare && d.proxied && (d.sslMode === "flexible" || d.sslMode === "off") && (
                  <p className="text-xs text-destructive">
                    This zone's SSL/TLS mode is <Mono>{d.sslMode}</Mono>: set it to Full (strict) in Cloudflare, or proxied
                    sites loop between HTTP and HTTPS.
                  </p>
                )}
              </div>
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
            </li>
          ))}
        </ul>
      )}
    </Section>
  );
}

/** Lets the manager keep DNS records (and tunnel routes) in sync. */
function CloudflareSection() {
  const qc = useQueryClient();
  const cf = useQuery({ queryKey: ["cloudflare"], queryFn: api.cloudflare, refetchInterval: 30_000 });
  const [token, setToken] = useState("");
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["cloudflare"] });
    qc.invalidateQueries({ queryKey: ["domains"] });
  };
  const connect = useMutation({
    meta: { error: "Couldn't connect Cloudflare" },
    mutationFn: () => api.connectCloudflare(token.trim()),
    onSuccess: () => {
      setToken("");
      refresh();
    },
  });
  const disconnect = useMutation({ meta: { error: "Couldn't disconnect Cloudflare" }, mutationFn: api.disconnectCloudflare, onSuccess: refresh });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    connect.mutate();
  };
  const status = cf.data;

  return (
    <Section
      title="Cloudflare"
      description={
        <>
          With an API token, kipitiny creates the DNS record of every service domain in your Cloudflare zones (an A record
          to the server, or a CNAME to your tunnel along with its route) and removes it when the domain goes away. Records
          it didn't create are never changed. Token permissions: <Mono>Zone › Zone › Read</Mono>,{" "}
          <Mono>Zone › DNS › Edit</Mono>, and <Mono>Account › Cloudflare Tunnel › Edit</Mono> with a tunnel.
        </>
      }
      actions={
        status?.connected && (
          <ConfirmDialog
            trigger={
              <Button variant="outline" size="sm">
                Disconnect
              </Button>
            }
            title="Disconnect Cloudflare?"
            description="kipitiny stops managing DNS. Records it created stay in Cloudflare."
            confirmLabel="Disconnect"
            onConfirm={() => disconnect.mutate()}
          />
        )
      }
    >
      {status?.connected ? (
        <div className="space-y-1 text-sm">
          <p>
            Connected · {status.zones.length} zone{status.zones.length === 1 ? "" : "s"}:{" "}
            <span className="font-mono">{status.zones.join(", ")}</span>
          </p>
          {status.tunnels.length > 0 && (
            <p className="text-muted-foreground">
              Tunnel routes managed for {status.tunnels.map((t) => t.server).join(", ")}.
            </p>
          )}
          {status.syncedAt && <p className="text-xs text-muted-foreground">Last sync {timeAgo(status.syncedAt)}</p>}
          {status.error && <p className="text-sm text-destructive">{status.error}</p>}
          {status.tunnelError && <p className="text-sm text-destructive">{status.tunnelError}</p>}
        </div>
      ) : (
        <form onSubmit={onSubmit} className="flex items-start gap-2">
          <FloatingInput
            label="API token"
            type="password"
            required
            autoComplete="off"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            className="flex-1"
          />
          <Button type="submit" className="h-14" disabled={connect.isPending}>
            Connect
          </Button>
        </form>
      )}
    </Section>
  );
}
