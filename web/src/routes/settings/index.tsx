import { Bell, BookOpen, Brush, GitFork, HardDrive, Globe, Info, KeyRound, KeySquare, LogIn, Mail, Network, Package, Palette, ScrollText, Server, Users, type LucideIcon } from "lucide-react";
import type { ComponentType } from "react";
import type { Feature } from "@/api";
import { EmptyState } from "@/components/common";
import { useAccess } from "@/lib/access";
import { docsUrl } from "@/lib/docs";
import { Version } from "./About";
import { Appearance } from "./Appearance";
import { Audit } from "./Audit";
import { Cleanup } from "./Cleanup";
import { DomainsPage } from "./Domains";
import { Email } from "./Email";
import { GitProviders } from "./GitProviders";
import { Networks } from "./Networks";
import { Notifications } from "./Notifications";
import { SettingsLabel, SettingsPage, SettingsReadOnly } from "./page";
import { PasswordManagers } from "./PasswordManagers";
import { Registries } from "./Registries";
import { Servers } from "./Servers";
import { SSOPage } from "./SSO";
import { Storage } from "./Storage";
import { TokensPage } from "./Tokens";
import { UsersPage } from "./Users";

export const settingsGroups = ["Infrastructure", "Integrations", "Access", "System"] as const;
type SettingsGroup = (typeof settingsGroups)[number];

type SettingsPage = {
  slug: string;
  /** The sidebar group it's listed under. */
  group: SettingsGroup;
  label: string;
  icon: LucideIcon;
  /** Who sees it besides admins: members with this feature, or every user; nobody else when absent. */
  access?: Feature | "everyone";
} & ({ page: ComponentType; href?: never } | { href: (version?: string) => string; page?: never });

export const settingsPages: SettingsPage[] = [
  {
    slug: "servers",
    access: "servers",
    group: "Infrastructure",
    label: "Servers",
    icon: Server,
    page: Servers,
  },
  {
    slug: "networks",
    access: "networks",
    group: "Infrastructure",
    label: "Networks",
    icon: Network,
    page: Networks,
  },
  {
    slug: "domains",
    access: "domains",
    group: "Infrastructure",
    label: "Domains & DNS",
    icon: Globe,
    page: DomainsPage,
  },
  {
    slug: "storage",
    access: "storage",
    group: "Integrations",
    label: "Storage",
    icon: HardDrive,
    page: Storage,
  },
  {
    slug: "git-providers",
    access: "git-providers",
    group: "Integrations",
    label: "Git providers",
    icon: GitFork,
    page: GitProviders,
  },
  {
    slug: "registries",
    access: "registries",
    group: "Integrations",
    label: "Registries",
    icon: Package,
    page: Registries,
  },
  {
    slug: "password-managers",
    access: "secret-providers",
    group: "Integrations",
    label: "Password managers",
    icon: KeyRound,
    page: PasswordManagers,
  },
  {
    slug: "tokens",
    access: "everyone",
    group: "Access",
    label: "API tokens & MCP",
    icon: KeySquare,
    page: TokensPage,
  },
  {
    slug: "notifications",
    access: "notifications",
    group: "Integrations",
    label: "Notifications",
    icon: Bell,
    page: Notifications,
  },
  {
    slug: "email",
    group: "Integrations",
    label: "Email",
    icon: Mail,
    page: Email,
  },
  {
    slug: "appearance",
    group: "System",
    label: "Appearance",
    icon: Palette,
    page: Appearance,
  },
  {
    slug: "cleanup",
    group: "System",
    label: "Cleanup",
    icon: Brush,
    page: Cleanup,
  },
  {
    slug: "docs",
    access: "everyone",
    group: "System",
    label: "Docs",
    icon: BookOpen,
    href: docsUrl,
  },
  {
    slug: "users",
    group: "Access",
    label: "Users",
    icon: Users,
    page: UsersPage,
  },
  {
    slug: "sso",
    group: "Access",
    label: "Single sign-on",
    icon: LogIn,
    page: SSOPage,
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
    access: "everyone",
    group: "System",
    label: "About",
    icon: Info,
    page: Version,
  },
];

/** Whether the signed-in user sees a settings page. */
export function useSettingsAccess() {
  const { admin, has } = useAccess();
  return (p: SettingsPage) => admin || p.access === "everyone" || (!!p.access && has(p.access));
}

/** One settings page; the sidebar lists the others. Each page renders its own header. */
export function SettingsShell({ slug }: { slug: string }) {
  const current = settingsPages.find((p) => p.slug === slug)!;
  const Page = current.page!;
  const { admin } = useAccess();
  const allowed = useSettingsAccess()(current);

  return (
    <SettingsLabel.Provider value={current.label}>
      {/* Members see the pages they were given read-only: an admin changes them. */}
      <SettingsReadOnly.Provider value={!admin && current.access !== "everyone"}>
        {allowed ? (
          <Page key={current.slug} />
        ) : (
          <SettingsPage>
            <EmptyState icon={current.icon} title="Admins only" description="Ask an admin if you need access to this page." />
          </SettingsPage>
        )}
      </SettingsReadOnly.Provider>
    </SettingsLabel.Provider>
  );
}
