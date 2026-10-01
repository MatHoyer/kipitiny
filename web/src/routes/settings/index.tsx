import { Bell, Brush, Globe, Info, KeyRound, KeySquare, Package, ScrollText, Server, type LucideIcon } from "lucide-react";
import type { ComponentType } from "react";
import { Navigate, useParams } from "react-router";
import { Version } from "./About";
import { Audit } from "./Audit";
import { Cleanup } from "./Cleanup";
import { DomainsPage } from "./Domains";
import { Notifications } from "./Notifications";
import { SettingsLabel } from "./page";
import { PasswordManagers } from "./PasswordManagers";
import { Registries } from "./Registries";
import { Servers } from "./Servers";
import { TokensPage } from "./Tokens";

type SettingsPage = {
  slug: string;
  label: string;
  icon: LucideIcon;
  page: ComponentType;
};

export const settingsPages: SettingsPage[] = [
  {
    slug: "servers",
    label: "Servers",
    icon: Server,
    page: Servers,
  },
  {
    slug: "domains",
    label: "Domains & DNS",
    icon: Globe,
    page: DomainsPage,
  },
  {
    slug: "registries",
    label: "Registries",
    icon: Package,
    page: Registries,
  },
  {
    slug: "password-managers",
    label: "Password managers",
    icon: KeyRound,
    page: PasswordManagers,
  },
  {
    slug: "tokens",
    label: "API tokens & MCP",
    icon: KeySquare,
    page: TokensPage,
  },
  {
    slug: "notifications",
    label: "Notifications",
    icon: Bell,
    page: Notifications,
  },
  {
    slug: "cleanup",
    label: "Cleanup",
    icon: Brush,
    page: Cleanup,
  },
  {
    slug: "audit",
    label: "Audit log",
    icon: ScrollText,
    page: Audit,
  },
  {
    slug: "about",
    label: "About",
    icon: Info,
    page: Version,
  },
];

/** One settings page; the sidebar lists the others. Each page renders its own header. */
export function SettingsShell() {
  const { page = "" } = useParams();
  const current = settingsPages.find((p) => p.slug === page);
  if (!current) return <Navigate to={`/settings/${settingsPages[0].slug}`} replace />;
  const Page = current.page;

  return (
    <SettingsLabel.Provider value={current.label}>
      <Page key={current.slug} />
    </SettingsLabel.Provider>
  );
}
