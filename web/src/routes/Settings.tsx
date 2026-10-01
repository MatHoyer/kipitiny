import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Trash2 } from "lucide-react";
import { useState, type FormEvent } from "react";
import { CheckboxField, CopyField, Empty, ErrorText, Mono, Section, Tag } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { PageBody, PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
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
import { FloatingSelect } from "@/components/ui/floating-select";
import { timeAgo } from "@/lib/format";
import { cn } from "@/lib/utils";
import { api, type Scope, type Status } from "../api";
import { Cleanup } from "./Cleanup";
import { Servers } from "./Servers";

const scopes: [Scope, string][] = [
  ["read", "Read: status, logs, backups list"],
  ["deploy", "Deploy: read + deploy, rollback, start/stop, back up"],
  ["admin", "Admin: everything, including settings, secrets and restores"],
];

export function Settings() {
  return (
    <>
      <PageHeader crumbs={[{ label: "Settings" }]} />
      <PageBody>
        <Version />
        <Domains />
        <CloudflareSection />
        <Servers />
        <Cleanup />
        <Tokens />
        <Audit />
      </PageBody>
    </>
  );
}

function Domains() {
  const qc = useQueryClient();
  const domains = useQuery({ queryKey: ["domains"], queryFn: api.domains });
  const [name, setName] = useState("");
  const refresh = () => qc.invalidateQueries({ queryKey: ["domains"] });
  const create = useMutation({
    mutationFn: () => api.createDomain(name),
    onSuccess: () => {
      setName("");
      refresh();
    },
  });
  const remove = useMutation({ mutationFn: api.deleteDomain, onSuccess: refresh });
  const proxy = useMutation({
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
      <ErrorText error={create.error} />
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
      <ErrorText error={remove.error ?? proxy.error} />
    </Section>
  );
}

/** The running version, with an on-demand check for a newer one. */
function Version() {
  const qc = useQueryClient();
  const status = useQuery({ queryKey: ["status"], queryFn: api.status, refetchInterval: 15_000 });
  const check = useMutation({
    mutationFn: api.checkUpdate,
    onSuccess: (update) => qc.setQueryData<Status>(["status"], (s) => s && { ...s, update }),
  });
  const update = status.data?.update;

  return (
    <Section
      title="Version"
      description="kipitiny looks for a newer release every six hours; the update is offered in the sidebar."
      actions={
        <Button variant="outline" size="sm" disabled={check.isPending} onClick={() => check.mutate()}>
          {check.isPending ? "Checking…" : "Check for updates"}
        </Button>
      }
    >
      {update && (
        <div className="space-y-1 text-sm">
          <p>
            Running <span className="font-mono">{update.current}</span>
            {update.available ? (
              <>
                {" "}
                · <span className="font-mono">{update.latest}</span> is available
              </>
            ) : (
              update.latest && " · up to date"
            )}
          </p>
          {update.available && !update.canApply && <p className="text-muted-foreground">{update.reason}</p>}
          {update.checkedAt && <p className="text-xs text-muted-foreground">Last check {timeAgo(update.checkedAt)}</p>}
          {update.error && <p className="text-sm text-destructive">{update.error}</p>}
        </div>
      )}
      <ErrorText error={check.error} />
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
    mutationFn: () => api.connectCloudflare(token.trim()),
    onSuccess: () => {
      setToken("");
      refresh();
    },
  });
  const disconnect = useMutation({ mutationFn: api.disconnectCloudflare, onSuccess: refresh });
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
      <ErrorText error={connect.error ?? disconnect.error} />
    </Section>
  );
}

function Tokens() {
  const qc = useQueryClient();
  const tokens = useQuery({ queryKey: ["tokens"], queryFn: api.tokens });
  const remove = useMutation({
    mutationFn: api.deleteToken,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["tokens"] }),
  });

  return (
    <Section
      title="API tokens & MCP"
      description={
        <>
          Tokens authenticate scripts (<Mono>Authorization: Bearer …</Mono> on <Mono>/api</Mono>) and AI agents on the
          MCP endpoint <Mono>{window.location.origin}/mcp</Mono>. Every change they make is in the audit log.
        </>
      }
      actions={<TokenDialog />}
    >
      {tokens.data?.length === 0 ? (
        <Empty>No tokens yet.</Empty>
      ) : (
        <ul className="-my-2 divide-y">
          {tokens.data?.map((t) => (
            <li key={t.id} className="flex items-center justify-between gap-4 py-2.5">
              <div>
                <p className="flex items-center gap-2 text-sm font-medium">
                  {t.name}
                  <Tag>{t.scope}</Tag>
                </p>
                <p className="text-xs text-muted-foreground">
                  created {timeAgo(t.createdAt)} · {t.lastUsedAt ? `last used ${timeAgo(t.lastUsedAt)}` : "never used"}
                </p>
              </div>
              <ConfirmDialog
                trigger={
                  <Button variant="ghost" size="icon-sm" title="Revoke" aria-label="Revoke" className="text-muted-foreground hover:text-destructive">
                    <Trash2 />
                  </Button>
                }
                title={`Revoke token ${t.name}?`}
                description="Anything using it stops working immediately."
                confirmLabel="Revoke"
                onConfirm={() => remove.mutate(t.id)}
              />
            </li>
          ))}
        </ul>
      )}
      <ErrorText error={remove.error} />
    </Section>
  );
}

function TokenDialog() {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [scope, setScope] = useState<Scope>("read");
  const [created, setCreated] = useState<string | null>(null);
  const create = useMutation({
    mutationFn: () => api.createToken(name.trim(), scope),
    onSuccess: (t) => {
      setCreated(t.token);
      qc.invalidateQueries({ queryKey: ["tokens"] });
    },
  });
  const onOpenChange = (next: boolean) => {
    setOpen(next);
    if (!next) {
      setName("");
      setScope("read");
      setCreated(null);
      create.reset();
    }
  };
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    create.mutate();
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button variant="outline" size="sm">
          <Plus data-icon="inline-start" />
          Create token
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-lg">
        {created ? (
          <>
            <DialogHeader>
              <DialogTitle>Token created</DialogTitle>
              <DialogDescription>Copy it now: it won't be shown again.</DialogDescription>
            </DialogHeader>
            <CopyField value={created} />
            <p className="text-sm text-muted-foreground">Connect Claude Code:</p>
            <CopyField
              value={`claude mcp add --transport http kipitiny ${window.location.origin}/mcp --header "Authorization: Bearer ${created}"`}
            />
            <DialogFooter>
              <Button onClick={() => onOpenChange(false)}>Done</Button>
            </DialogFooter>
          </>
        ) : (
          <form onSubmit={onSubmit} className="contents">
            <DialogHeader>
              <DialogTitle>New API token</DialogTitle>
              <DialogDescription>For a script or an AI agent.</DialogDescription>
            </DialogHeader>
            <div className="space-y-4">
              <FloatingInput label="Name" required autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="claude-code" />
              <FloatingSelect
                label="Scope"
                value={scope}
                onValueChange={(v) => setScope(v as Scope)}
                options={scopes.map(([value, label]) => ({ value, label }))}
              />
              <ErrorText error={create.error} />
            </div>
            <DialogFooter>
              <Button type="submit" disabled={create.isPending}>
                Create token
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}

function Audit() {
  const audit = useQuery({ queryKey: ["audit"], queryFn: api.audit, refetchInterval: 15_000 });
  const th = "px-2 pb-2 font-medium";
  return (
    <Section title="Audit log" description="Every change, by whom, and how it went.">
      <ErrorText error={audit.error} />
      {audit.data?.length === 0 ? (
        <Empty>Nothing yet.</Empty>
      ) : (
        <div className="-mx-2 max-h-96 overflow-auto">
          <table className="w-full text-left text-xs">
            <thead className="sticky top-0 bg-card text-muted-foreground">
              <tr className="border-b">
                <th className={th}>When</th>
                <th className={th}>Who</th>
                <th className={th}>Action</th>
                <th className={th}>Result</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {audit.data?.map((e) => (
                <tr key={e.id} className="hover:bg-muted/50">
                  <td className="px-2 py-1.5 whitespace-nowrap text-muted-foreground" title={new Date(e.createdAt).toLocaleString()}>
                    {timeAgo(e.createdAt)}
                  </td>
                  <td className="px-2 py-1.5">{e.actor}</td>
                  <td className="px-2 py-1.5 font-mono">
                    {e.action}
                    {e.target && <span className="text-muted-foreground"> {e.target}</span>}
                  </td>
                  <td className={cn("px-2 py-1.5", e.status >= 400 ? "text-destructive" : "text-muted-foreground")} title={e.error}>
                    {e.status}
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
