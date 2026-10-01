import { useQuery } from "@tanstack/react-query";
import { Bell, Brush, Globe, Info, KeyRound, KeySquare, ScrollText, Server, type LucideIcon } from "lucide-react";
import type { ComponentType } from "react";
import { Navigate, useParams } from "react-router";
import { LinkCard } from "@/components/common";
import { PageBody, PageHeader } from "@/components/page-header";
import { timeAgo } from "@/lib/format";
import { api } from "@/api";
import { Version } from "./About";
import { Audit } from "./Audit";
import { Cleanup } from "./Cleanup";
import { DomainsPage } from "./Domains";
import { Notifications } from "./Notifications";
import { PasswordManagers } from "./PasswordManagers";
import { Servers } from "./Servers";
import { TokensPage } from "./Tokens";

type SettingsPage = {
  slug: string;
  label: string;
  icon: LucideIcon;
  description: string;
  page: ComponentType;
  /** A live one-liner for the hub card. */
  summary: ComponentType;
};

const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? "" : "s"}`;

export const settingsPages: SettingsPage[] = [
  {
    slug: "servers",
    label: "Servers",
    icon: Server,
    description: "Docker hosts projects run on, local or over SSH.",
    page: Servers,
    summary: () => {
      const servers = useQuery({ queryKey: ["servers"], queryFn: api.servers });
      if (!servers.data) return "…";
      const down = servers.data.filter((s) => !s.docker).length;
      return `${plural(servers.data.length, "server")} · ${down ? `${down} unreachable` : "all reachable"}`;
    },
  },
  {
    slug: "domains",
    label: "Domains & DNS",
    icon: Globe,
    description: "Your domains, and Cloudflare DNS and tunnels kept in sync.",
    page: DomainsPage,
    summary: () => {
      const domains = useQuery({ queryKey: ["domains"], queryFn: api.domains });
      const cf = useQuery({ queryKey: ["cloudflare"], queryFn: api.cloudflare });
      if (!domains.data) return "…";
      return `${plural(domains.data.length, "domain")} · Cloudflare ${cf.data?.connected ? "connected" : "not connected"}`;
    },
  },
  {
    slug: "password-managers",
    label: "Password managers",
    icon: KeyRound,
    description: "Reference secrets in env instead of pasting them.",
    page: PasswordManagers,
    summary: () => {
      const providers = useQuery({ queryKey: ["secret-providers"], queryFn: api.secretProviders });
      if (!providers.data) return "…";
      const connected = providers.data.filter((p) => p.connected).map((p) => p.name);
      return connected.length ? `${connected.join(", ")} connected` : "None connected";
    },
  },
  {
    slug: "tokens",
    label: "API tokens & MCP",
    icon: KeySquare,
    description: "Tokens for scripts and AI agents, and the MCP endpoint.",
    page: TokensPage,
    summary: () => {
      const tokens = useQuery({ queryKey: ["tokens"], queryFn: api.tokens });
      return tokens.data ? plural(tokens.data.length, "token") : "…";
    },
  },
  {
    slug: "notifications",
    label: "Notifications",
    icon: Bell,
    description: "Where failed deploys, backups and restarts are reported.",
    page: Notifications,
    summary: () => {
      const n = useQuery({ queryKey: ["notifications"], queryFn: api.notifications });
      return n.data ? plural(n.data.channels.length, "channel") : "…";
    },
  },
  {
    slug: "cleanup",
    label: "Cleanup",
    icon: Brush,
    description: "Free disk space Docker leaves behind, on a schedule.",
    page: Cleanup,
    summary: () => {
      const c = useQuery({ queryKey: ["cleanup"], queryFn: api.cleanup });
      if (!c.data) return "…";
      if (c.data.running) return "Running now";
      return c.data.lastRun ? `Last run ${timeAgo(c.data.lastRun.startedAt)}` : "Never run";
    },
  },
  {
    slug: "audit",
    label: "Audit log",
    icon: ScrollText,
    description: "Every change, by whom, and how it went.",
    page: Audit,
    summary: () => {
      const audit = useQuery({ queryKey: ["audit"], queryFn: api.audit });
      if (!audit.data) return "…";
      if (!audit.data.length) return "Nothing yet";
      const failed = audit.data.filter((e) => e.status >= 400).length;
      return `Last change ${timeAgo(audit.data[0].createdAt)}${failed ? ` · ${failed} failed` : ""}`;
    },
  },
  {
    slug: "about",
    label: "About",
    icon: Info,
    description: "The running version and updates.",
    page: Version,
    summary: () => {
      const status = useQuery({ queryKey: ["status"], queryFn: api.status });
      const u = status.data?.update;
      if (!status.data) return "…";
      return u?.available ? `${status.data.version} · ${u.latest} available` : `${status.data.version} · up to date`;
    },
  },
];

export function SettingsHub() {
  return (
    <>
      <PageHeader crumbs={[{ label: "Settings" }]} />
      <PageBody>
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {settingsPages.map((p) => (
            <LinkCard
              key={p.slug}
              to={`/settings/${p.slug}`}
              icon={p.icon}
              title={p.label}
              description={p.description}
              meta={<p.summary />}
            />
          ))}
        </div>
      </PageBody>
    </>
  );
}

/** One settings page; the sidebar lists the others. */
export function SettingsShell() {
  const { page = "" } = useParams();
  const current = settingsPages.find((p) => p.slug === page);
  if (!current) return <Navigate to="/settings" replace />;
  const Page = current.page;

  return (
    <>
      <PageHeader crumbs={[{ label: "Settings", to: "/settings" }, { label: current.label }]} />
      <PageBody>
        <Page key={current.slug} />
      </PageBody>
    </>
  );
}
