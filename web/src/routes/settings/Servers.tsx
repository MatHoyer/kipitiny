import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  ChevronRight,
  Cloud,
  FolderKanban,
  Globe,
  HardDrive,
  Plus,
  Server as ServerIcon,
  SquareTerminal,
  Trash2,
} from "lucide-react";
import { lazy, Suspense, useId, useState, type FormEvent, type ReactNode } from "react";
import { Link, Navigate, useNavigate, useParams } from "react-router";
import { toast } from "sonner";
import {
  CheckboxField,
  CopyField,
  DangerZone,
  ErrorText,
  IconTile,
  Mono,
  Section,
  StatCard,
  StateBadge,
  Tag,
  Loading,
} from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { PageBody, PageHeader } from "@/components/page-header";
import { SaveBar } from "@/components/save-bar";
import { memoryOf, useUsage } from "@/components/usage";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
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
import { api, SECRET_MASK, type Server, type ServerInput, type ServiceUsage } from "@/api";
import { formatBytes } from "@/lib/format";
import { SettingsPage } from "./page";

const address = (s: Server) => (s.kind === "local" ? "the manager's own Docker" : `ssh://${s.sshUser}@${s.host}:${s.port}`);

/** How the server's apps are reached from the internet. */
const exposure = (s: Server) =>
  s.tunnel ? "Cloudflare tunnel" : s.publicIp ? `IP ${s.publicIp}` : s.detectedIp ? `IP ${s.detectedIp} (detected)` : "Public ports";

export function Servers() {
  const servers = useQuery({ queryKey: ["servers"], queryFn: api.servers, refetchInterval: 30_000 });
  return (
    <SettingsPage actions={<NewServerDialog />}>
      <ErrorText error={servers.error} />
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {servers.data?.map((s) => (
          <ServerCard key={s.id} server={s} />
        ))}
      </div>
    </SettingsPage>
  );
}

function ServerCard({ server: s }: { server: Server }) {
  return (
    <Link to={`/servers/${s.id}`} className="group rounded-xl outline-none focus-visible:ring-3 focus-visible:ring-ring/50">
      <Card className="h-full gap-3 px-4 transition-all group-hover:-translate-y-px group-hover:shadow-md group-hover:ring-foreground/20">
        <div className="flex items-start gap-3">
          <IconTile icon={s.kind === "local" ? HardDrive : ServerIcon} />
          <div className="min-w-0 flex-1">
            <p className="truncate font-medium">{s.name}</p>
            <p className="truncate font-mono text-xs text-muted-foreground">{address(s)}</p>
          </div>
          <StateBadge state={s.docker ? "running" : "failed"} />
        </div>
        <div className="mt-auto flex flex-wrap gap-1.5">
          <Tag>{s.docker ? `Docker ${s.docker.version} · ${s.docker.arch}` : "Docker unreachable"}</Tag>
          <Tag>{`${s.projects} project${s.projects === 1 ? "" : "s"}`}</Tag>
          <Tag className="flex items-center gap-1">
            {s.tunnel ? <Cloud className="size-3" /> : <Globe className="size-3" />}
            {exposure(s)}
          </Tag>
        </div>
      </Card>
    </Link>
  );
}

/** One server: its status, how it's reached, its SSH connection and its projects. */
export function ServerPage() {
  const { id = "" } = useParams();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const servers = useQuery({ queryKey: ["servers"], queryFn: api.servers, refetchInterval: 30_000 });
  const projects = useQuery({ queryKey: ["projects"], queryFn: api.projects });
  const usage = useUsage();
  const remove = useMutation({
    meta: { error: "Couldn't remove the server" },
    mutationFn: api.deleteServer,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["servers"] });
      navigate("/servers");
    },
  });
  const s = servers.data?.find((x) => x.id === id);
  const crumbs = [{ label: "Servers", to: "/servers" }, { label: s?.name ?? <Spinner className="size-3.5" /> }];

  if (servers.data && !s) return <Navigate to="/servers" replace />;
  if (!s)
    return (
      <>
        <PageHeader crumbs={crumbs} />
        <PageBody>{servers.error ? <ErrorText error={servers.error} /> : <Loading />}</PageBody>
      </>
    );

  const own = projects.data?.filter((p) => p.serverId === s.id) ?? [];
  const memory = (keep: (u: ServiceUsage) => boolean) => memoryOf(usage.data, keep);
  const total = memory((u) => u.serverId === s.id);

  return (
    <>
      <PageHeader crumbs={crumbs} />
      <PageBody>
        <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
          <StatCard
            icon={Activity}
            label="Docker"
            value={s.docker ? s.docker.version : "down"}
            hint={s.docker ? `${s.docker.os} · ${s.docker.arch}` : s.dockerError}
            tone={s.docker ? "good" : "bad"}
          />
          <StatCard
            icon={FolderKanban}
            label="Projects"
            value={s.projects}
            hint={total !== undefined && `${formatBytes(total)} memory in use`}
          />
          <StatCard icon={s.kind === "local" ? HardDrive : ServerIcon} label="Connection" value={s.kind === "local" ? "Local" : "SSH"} hint={address(s)} />
          <StatCard icon={s.tunnel ? Cloud : Globe} label="Reached through" value={s.tunnel ? "Tunnel" : "IP"} hint={exposure(s)} />
        </div>
        {s.dockerError && (
          <p role="alert" className="text-sm text-destructive">
            {s.dockerError}
          </p>
        )}

        <NetworkSection key={`net-${s.id}`} server={s} />
        {s.kind === "ssh" && <ConnectionSection key={`ssh-${s.id}`} server={s} />}

        <Section title="Projects" description={own.length === 0 ? "No project runs here yet." : undefined}>
          {own.length > 0 && (
            <ul className="-my-2 divide-y">
              {own.map((p) => (
                <li key={p.id}>
                  <Link to={`/projects/${p.id}`} className="flex items-center gap-2 py-2.5 text-sm hover:underline">
                    <FolderKanban className="size-4 text-muted-foreground" />
                    {p.name}
                    <span className="ml-auto text-xs text-muted-foreground tabular-nums">
                      {formatBytes(memory((u) => u.projectId === p.id) ?? 0)}
                    </span>
                    <ChevronRight className="size-4 text-muted-foreground" />
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </Section>

        <HostTerminal key={`term-${s.id}`} server={s} />

        {s.kind === "ssh" && (
          <DangerZone description={s.projects > 0 ? "Move or delete its projects first." : "kipitiny stops managing it and removes the Traefik and tunnel containers it put there."}>
            <ConfirmDialog
              trigger={
                <Button variant="destructive" size="sm" disabled={remove.isPending || s.projects > 0}>
                  <Trash2 data-icon="inline-start" />
                  Remove server
                </Button>
              }
              title={`Remove server ${s.name}?`}
              confirmLabel="Remove"
              onConfirm={() => remove.mutate(s.id)}
            />
          </DangerZone>
        )}
      </PageBody>
    </>
  );
}

/** How a server's apps are reached: a Cloudflare tunnel, or its public IP. */
function NetworkSection({ server }: { server: Server }) {
  const qc = useQueryClient();
  const [ip, setIp] = useState(server.publicIp);
  const [token, setToken] = useState(server.tunnel === "server" ? SECRET_MASK : "");
  const save = useMutation({
    meta: { error: "Couldn't save the network settings" },
    mutationFn: () => api.setServerNetwork(server.id, { publicIp: ip.trim(), tunnelToken: token.trim() }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["servers"] });
      qc.invalidateQueries({ queryKey: ["cloudflare"] });
      setIp(ip.trim());
      setToken(token.trim() ? SECRET_MASK : "");
      toast.success("Network settings saved");
    },
  });
  const formId = useId();
  const dirty = ip !== server.publicIp || token !== (server.tunnel === "server" ? SECRET_MASK : "");
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <Section title="Network" description="How its apps are reached from the internet.">
      <form id={formId} onSubmit={onSubmit} className="space-y-4">
        <div className="grid gap-4 md:grid-cols-2">
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
      </form>
      <SaveBar
        form={formId}
        dirty={dirty}
        saving={save.isPending}
        onReset={() => {
          setIp(server.publicIp);
          setToken(server.tunnel === "server" ? SECRET_MASK : "");
        }}
      />
    </Section>
  );
}

function useServerForm(server: Server | null, onSaved: (s: Server) => void) {
  const qc = useQueryClient();
  const initial = {
    name: server?.name ?? "",
    host: server?.host ?? "",
    port: String(server?.port ?? 22),
    sshUser: server?.sshUser ?? "root",
    socket: server?.socket ?? "/var/run/docker.sock",
  };
  const [form, setForm] = useState(initial);
  const [resetHostKey, setResetHostKey] = useState(false);
  const save = useMutation({
    meta: { error: "Couldn't save the server" },
    mutationFn: () => {
      const input: ServerInput = { ...form, port: Number(form.port) || 22, resetHostKey };
      return server ? api.updateServer(server.id, input) : api.createServer(input);
    },
    onSuccess: (s) => {
      qc.invalidateQueries({ queryKey: ["servers"] });
      setResetHostKey(false);
      onSaved(s);
    },
  });
  const dirty = resetHostKey || JSON.stringify(form) !== JSON.stringify(initial);
  const reset = () => {
    setForm(initial);
    setResetHostKey(false);
  };
  return { form, setForm, resetHostKey, setResetHostKey, save, dirty, reset };
}

/** What the server needs before the manager can reach it. */
function SshSetup() {
  const key = useQuery({ queryKey: ["ssh-key"], queryFn: api.sshKey });
  return (
    <div className="space-y-2">
      <p className="text-sm text-muted-foreground">
        On the server, add the manager's key to <Mono>~/.ssh/authorized_keys</Mono> of the SSH user (who needs access to
        the Docker socket).
      </p>
      {key.data ? <CopyField value={key.data.publicKey} /> : <Loading className="py-2" />}
      <p className="text-xs text-muted-foreground">
        sshd must allow <Mono>AllowTcpForwarding yes</Mono> (or <Mono>local</Mono>) and{" "}
        <Mono>AllowStreamLocalForwarding yes</Mono>: the Docker API is reached through the socket, never over TCP. The
        server's host key is pinned on first connection.
      </p>
    </div>
  );
}

function ServerFields({
  f,
  hostKey,
  autoFocus,
}: {
  f: ReturnType<typeof useServerForm>;
  hostKey?: boolean;
  autoFocus?: boolean;
}): ReactNode {
  const set = (k: keyof typeof f.form) => (e: { target: { value: string } }) => f.setForm({ ...f.form, [k]: e.target.value });
  return (
    <div className="grid gap-4 sm:grid-cols-2">
      <FloatingInput label="Name" required autoFocus={autoFocus} value={f.form.name} onChange={set("name")} placeholder="db-server-2" />
      <FloatingInput label="Host" required value={f.form.host} onChange={set("host")} placeholder="203.0.113.10" />
      <FloatingInput label="SSH port" type="number" min={1} max={65535} value={f.form.port} onChange={set("port")} />
      <FloatingInput label="SSH user" required value={f.form.sshUser} onChange={set("sshUser")} />
      <FloatingInput
        label="Docker socket"
        required
        value={f.form.socket}
        onChange={set("socket")}
        inputClassName="font-mono"
        className="sm:col-span-2"
      />
      {hostKey && (
        <CheckboxField
          label="Forget the pinned host key"
          description="Only after reinstalling the server."
          checked={f.resetHostKey}
          onCheckedChange={f.setResetHostKey}
          className="sm:col-span-2"
        />
      )}
    </div>
  );
}

function ConnectionSection({ server }: { server: Server }) {
  const f = useServerForm(server, () => toast.success("Server saved"));
  const formId = useId();
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    f.save.mutate();
  };
  return (
    <Section title="SSH connection">
      <form id={formId} onSubmit={onSubmit} className="space-y-4">
        <ServerFields f={f} hostKey={!!server.hostKey} />
        <SshSetup />
      </form>
      <SaveBar form={formId} dirty={f.dirty} saving={f.save.isPending} onReset={f.reset} />
    </Section>
  );
}

function NewServerDialog() {
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const f = useServerForm(null, (s) => {
    setOpen(false);
    navigate(`/servers/${s.id}`);
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    f.save.mutate();
  };
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm">
          <Plus data-icon="inline-start" />
          Add server
        </Button>
      </DialogTrigger>
      <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-lg">
        <form onSubmit={onSubmit} className="contents">
          <DialogHeader>
            <DialogTitle>New server</DialogTitle>
            <DialogDescription>A Docker host reached over SSH.</DialogDescription>
          </DialogHeader>
          <SshSetup />
          <ServerFields f={f} autoFocus />
          <DialogFooter>
            <Button type="submit" loading={f.save.isPending}>
              Add server
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// xterm.js is big: load it with the first terminal.
const TerminalView = lazy(() => import("@/components/terminal").then((m) => ({ default: m.TerminalView })));

/** A root shell on the host, through a privileged helper container. */
function HostTerminal({ server }: { server: Server }) {
  // Each connect mounts a new session; an ended one stays on screen.
  const [session, setSession] = useState(0);
  const [live, setLive] = useState(false);

  return (
    <Section
      title="Terminal"
      description="Root shell on the host. Sessions are recorded in the audit log."
      actions={
        live ? (
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              setSession(0);
              setLive(false);
            }}
          >
            Disconnect
          </Button>
        ) : (
          <Button
            size="sm"
            disabled={!server.docker}
            onClick={() => {
              setSession((n) => n + 1);
              setLive(true);
            }}
          >
            <SquareTerminal data-icon="inline-start" />
            {session ? "Reconnect" : "Connect"}
          </Button>
        )
      }
    >
      {session > 0 && (
        <Suspense fallback={<Loading />}>
          <TerminalView key={session} path={api.serverTerminalUrl(server.id)} onClose={() => setLive(false)} className="h-[28rem]" />
        </Suspense>
      )}
    </Section>
  );
}
