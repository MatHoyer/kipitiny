import { Bell, Brush, HardDrive, Globe, Info, KeyRound, KeySquare, Package, ScrollText, Server, type LucideIcon } from "lucide-react";
import type { ComponentType } from "react";
import { Version } from "./About";
import { Audit } from "./Audit";
import { Cleanup } from "./Cleanup";
import { DomainsPage } from "./Domains";
import { Notifications } from "./Notifications";
import { SettingsLabel } from "./page";
import { PasswordManagers } from "./PasswordManagers";
import { Registries } from "./Registries";
import { Servers } from "./Servers";
import { Storage } from "./Storage";
import { TokensPage } from "./Tokens";

export const settingsGroups = ["Infrastructure", "Integrations", "Access", "System"] as const;
type SettingsGroup = (typeof settingsGroups)[number];

type SettingsPage = {
  slug: string;
  /** The sidebar group it's listed under. */
  group: SettingsGroup;
  label: string;
  icon: LucideIcon;
  page: ComponentType;
};

export const settingsPages: SettingsPage[] = [
  {
    slug: "servers",
    group: "Infrastructure",
    label: "Servers",
    icon: Server,
    page: Servers,
  },
  {
    slug: "domains",
    group: "Infrastructure",
    label: "Domains & DNS",
    icon: Globe,
    page: DomainsPage,
  },
  {
    slug: "storage",
    group: "Infrastructure",
    label: "Storage",
    icon: HardDrive,
    page: Storage,
  },
  {
    slug: "registries",
    group: "Integrations",
    label: "Registries",
    icon: Package,
    page: Registries,
  },
  {
    slug: "password-managers",
    group: "Integrations",
    label: "Password managers",
    icon: KeyRound,
    page: PasswordManagers,
  },
  {
    slug: "tokens",
    group: "Access",
    label: "API tokens & MCP",
    icon: KeySquare,
    page: TokensPage,
  },
  {
    slug: "notifications",
    group: "Integrations",
    label: "Notifications",
    icon: Bell,
    page: Notifications,
  },
  {
    slug: "cleanup",
    group: "System",
    label: "Cleanup",
    icon: Brush,
    page: Cleanup,
  },
  {
    slug: "audit",
    group: "Access",
    label: "Audit log",
    icon: ScrollText,
    page: Audit,
  },
  {
    slug: "about",
    group: "System",
    label: "About",
    icon: Info,
    page: Version,
  },
];

/** One settings page; the sidebar lists the others. Each page renders its own header. */
export function SettingsShell({ slug }: { slug: string }) {
  const current = settingsPages.find((p) => p.slug === slug)!;
  const Page = current.page;

  return (
    <SettingsLabel.Provider value={current.label}>
      <Page key={current.slug} />
    </SettingsLabel.Provider>
  );
}
