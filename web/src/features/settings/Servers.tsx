import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, ChevronRight, Cloud, FolderKanban, Globe, HardDrive, Server as ServerIcon, Trash2 } from "lucide-react";
import { Link, Navigate, useNavigate, useParams } from "react-router";
import { ErrorText, StateBadge, Tag } from "@/components/common";
import { DangerZone } from "@kipit-labs/kipitidy/danger-zone";
import { IconTile } from "@kipit-labs/kipitidy/icon-tile";
import { Loading } from "@kipit-labs/kipitidy/loading";
import { Section } from "@kipit-labs/kipitidy/section";
import { StatCard } from "@kipit-labs/kipitidy/stat-card";
import { ConfirmDialog } from "@kipit-labs/kipitidy/confirm-dialog";
import { PageBody, PageHeader } from "@/features/layout/page-header";
import { ServerSecuritySection } from "@/features/settings/server-security";
import { HostUsageSection, memoryOf, useUsage } from "@/features/services/usage";
import { useAccess } from "@/lib/access";
import { Button } from "@kipit-labs/kipitidy/button";
import { Spinner } from "@kipit-labs/kipitidy/spinner";
import { Card } from "@kipit-labs/kipitidy/card";
import { api, type Server, type ServiceUsage } from "@/api";
import { formatBytes } from "@/lib/format";
import { SettingsPage } from "./page";
import { HostTerminal } from "./host-terminal";
import { ConnectionSection, NewServerDialog } from "./server-connection";
import { NetworkSection } from "./server-network";

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
  const { admin } = useAccess();
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
      <PageHeader crumbs={crumbs} actions={admin && <HostTerminal key={`term-${s.id}`} server={s} />} />
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

        <HostUsageSection serverId={s.id} containers={total} />

        {admin && <ServerSecuritySection key={`sec-${s.id}`} serverId={s.id} />}

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
