import { useQuery } from "@tanstack/react-query";
import {
  ArchiveRestore,
  Bell,
  Check,
  Database,
  DatabaseBackup,
  DatabaseZap,
  Download,
  Eye,
  Folder,
  FolderPen,
  FolderPlus,
  GitBranch,
  GitFork,
  Globe,
  HardDrive,
  KeyRound,
  Network,
  Package,
  Rocket,
  Server,
  Settings2,
  ShieldCheck,
  SquareTerminal,
  Trash2,
  UserRound,
  Variable,
  type LucideIcon,
} from "lucide-react";
import { useState } from "react";
import { Segmented, SegmentedItem } from "@/components/ui/segmented";
import { Switch } from "@/components/ui/switch";
import { Tip } from "@/components/ui/tooltip";
import type { useAccess } from "@/lib/access";
import { cn } from "@/lib/utils";
import { api, features, perms, type Feature, type Perm, type UserAccess } from "@/api";

type Labelled = { icon: LucideIcon; label: string; description: string };

export const permLabels: Record<Perm, Labelled> = {
  view: { icon: Eye, label: "View", description: "Services, logs, stats, deployments and backups" },
  deploy: { icon: Rocket, label: "Deploy", description: "Deploy, roll back, start, stop, maintenance" },
  settings: { icon: Settings2, label: "Settings", description: "Create and edit services, compose files, networks, uptime checks" },
  env: { icon: Variable, label: "Env", description: "Variables, secrets and connection strings" },
  domains: { icon: Globe, label: "Domains", description: "Public domains and their middlewares" },
  backups: { icon: DatabaseBackup, label: "Backups", description: "Back up now, verify, schedules" },
  restore: { icon: ArchiveRestore, label: "Restore", description: "Replace data with a backup" },
  download: { icon: Download, label: "Download", description: "Backup files and exports with secrets" },
  data: { icon: Database, label: "Data", description: "Browse databases, read-only console" },
  "data-write": { icon: DatabaseZap, label: "Data write", description: "Edit rows, schema and run writing queries" },
  files: { icon: Folder, label: "Files", description: "Browse volume files" },
  "files-write": { icon: FolderPen, label: "Files write", description: "Upload, edit, move and delete volume files" },
  terminal: { icon: SquareTerminal, label: "Terminal", description: "A shell in the containers" },
  git: { icon: GitBranch, label: "Git", description: "Link, sync and unlink a repository" },
  delete: { icon: Trash2, label: "Delete", description: "Delete services and the project" },
};

/** The permissions as the custom editor shows them, by what they touch. */
const permGroups: { label: string; perms: Perm[] }[] = [
  { label: "Run", perms: ["view", "deploy", "terminal"] },
  { label: "Configure", perms: ["settings", "env", "domains", "git"] },
  { label: "Data", perms: ["data", "data-write", "files", "files-write"] },
  { label: "Backups", perms: ["backups", "restore", "download"] },
  { label: "Danger", perms: ["delete"] },
];

/** One-click permission sets; anything else is "custom". */
const presets: { value: string; label: string; perms: readonly Perm[] }[] = [
  { value: "none", label: "None", perms: [] },
  { value: "view", label: "View", perms: ["view"] },
  { value: "deploy", label: "Deploy", perms: ["view", "deploy"] },
  { value: "full", label: "Full", perms },
];

const presetOf = (ps: Perm[]) =>
  presets.find((p) => p.perms.length === ps.length && p.perms.every((q) => ps.includes(q)))?.value ?? "custom";

/** What members may see on the server, read-only; create-projects is a capability, shown on its own. */
const featureLabels: Record<Feature, Labelled> = {
  "create-projects": { icon: FolderPlus, label: "Create projects", description: "With every permission on the ones they create" },
  storage: { icon: HardDrive, label: "Storage", description: "Pick where backups go" },
  "git-providers": { icon: GitFork, label: "Git providers", description: "Pick one when linking a repository" },
  registries: { icon: Package, label: "Registries", description: "Which registries pulls can use" },
  "secret-providers": { icon: KeyRound, label: "Password managers", description: "Reference their items in env" },
  servers: { icon: Server, label: "Servers", description: "Without how to reach them" },
  networks: { icon: Network, label: "Networks", description: "The ones created by hand" },
  notifications: { icon: Bell, label: "Notifications", description: "Without where they send" },
  domains: { icon: Globe, label: "Domains", description: "The base domains" },
};

type RoleText = { title: string; description: string };

const userRoles: Record<UserAccess["role"], RoleText> = {
  member: { title: "Member", description: "Sees and does only what you grant below." },
  admin: { title: "Admin", description: "Everything: every project, server settings, users, audit log." },
};

/** Why an option is off when the editor is limited to what the signed-in user holds. */
const notYours = "You don't have this yourself";

/**
 * A role, and for a member the server features and, project by project, a preset or custom permissions. roles
 * renames the two choices; limit (the signed-in user's access) disables what they couldn't give, as for their
 * own tokens.
 */
export function AccessEditor({
  value,
  onChange,
  roles = userRoles,
  limit,
}: {
  value: UserAccess;
  onChange: (a: UserAccess) => void;
  roles?: Record<UserAccess["role"], RoleText>;
  limit?: ReturnType<typeof useAccess>;
}) {
  const projects = useQuery({ queryKey: ["projects"], queryFn: api.projects });
  const can = (projectId: string, p: Perm) => !limit || limit.can(projectId, p);
  const has = (f: Feature) => !limit || limit.has(f);
  // Projects whose custom editor is open, though their permissions may match a preset.
  const [custom, setCustom] = useState<Set<string>>(
    () => new Set(Object.entries(value.grants).flatMap(([id, ps]) => (presetOf(ps) === "custom" ? [id] : []))),
  );
  const setPerms = (projectId: string, ps: readonly Perm[]) => {
    const grants = { ...value.grants };
    if (ps.length) grants[projectId] = perms.filter((p) => ps.includes(p));
    else delete grants[projectId];
    onChange({ ...value, grants });
  };
  const toggle = (projectId: string, p: Perm) => {
    const cur = value.grants[projectId] ?? [];
    const on = !cur.includes(p);
    let next = on ? [...cur, p] : cur.filter((q) => q !== p);
    if (on && p !== "view" && !next.includes("view")) next = ["view", ...next];
    if (!on && p === "view") next = []; // every permission implies view
    setPerms(projectId, next);
  };
  const setFeature = (f: Feature, on: boolean) =>
    onChange({ ...value, features: on ? [...value.features, f] : value.features.filter((x) => x !== f) });
  const seeable = features.filter((f) => f !== "create-projects");
  const createProjects = featureLabels["create-projects"];

  return (
    <div className="-mx-1 max-h-[65vh] space-y-6 overflow-y-auto px-1 pb-1">
      <div role="radiogroup" aria-label="Role" className="grid gap-3 sm:grid-cols-2">
        <RoleCard
          icon={UserRound}
          {...roles.member}
          selected={value.role === "member"}
          onSelect={() => onChange({ ...value, role: "member" })}
        />
        <RoleCard
          icon={ShieldCheck}
          {...roles.admin}
          selected={value.role === "admin"}
          onSelect={() => onChange({ ...value, role: "admin" })}
        />
      </div>
      {value.role === "member" && (
        <>
          <section className="space-y-3">
            <div>
              <h3 className="text-sm font-medium">Projects</h3>
              <p className="text-xs text-muted-foreground">Every permission implies View.</p>
            </div>
            {projects.data?.length === 0 ? (
              <p className="text-sm text-muted-foreground">No projects yet.</p>
            ) : (
              <ul className="divide-y rounded-lg border">
                {projects.data?.map((pr) => {
                  const cur = value.grants[pr.id] ?? [];
                  const open = custom.has(pr.id) || presetOf(cur) === "custom";
                  return (
                    <li key={pr.id} className="space-y-3 px-3 py-2.5">
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <span className="min-w-0 truncate text-sm font-medium">{pr.name}</span>
                        <Segmented
                          size="sm"
                          aria-label={`Access to ${pr.name}`}
                          value={open ? "custom" : presetOf(cur)}
                          onValueChange={(v) => {
                            const next = new Set(custom);
                            if (v === "custom") next.add(pr.id);
                            else {
                              next.delete(pr.id);
                              setPerms(pr.id, presets.find((p) => p.value === v)!.perms);
                            }
                            setCustom(next);
                          }}
                        >
                          {presets.map((p) => (
                            <SegmentedItem key={p.value} value={p.value} disabled={!p.perms.every((q) => can(pr.id, q))}>
                              {p.label}
                            </SegmentedItem>
                          ))}
                          <SegmentedItem value="custom">Custom</SegmentedItem>
                        </Segmented>
                      </div>
                      {open && (
                        <div className="space-y-2 rounded-md bg-muted/40 p-2.5">
                          {permGroups.map((g) => (
                            <div key={g.label} className="flex items-start gap-2">
                              <span className="w-20 shrink-0 pt-1.5 text-xs text-muted-foreground">{g.label}</span>
                              <div className="flex flex-1 flex-wrap gap-1.5">
                                {g.perms.map((p) => (
                                  <PermChip
                                    key={p}
                                    perm={p}
                                    on={cur.includes(p)}
                                    disabled={!can(pr.id, p) && !cur.includes(p)}
                                    onToggle={() => toggle(pr.id, p)}
                                  />
                                ))}
                              </div>
                            </div>
                          ))}
                        </div>
                      )}
                    </li>
                  );
                })}
              </ul>
            )}
          </section>
          <section className="space-y-3">
            <div>
              <h3 className="text-sm font-medium">Server</h3>
              <p className="text-xs text-muted-foreground">What may be seen in settings, read-only and without secrets.</p>
            </div>
            <Tip content={!has("create-projects") && notYours}>
              <label
                className={cn(
                  "flex cursor-pointer items-center gap-3 rounded-lg border px-3 py-2.5",
                  !has("create-projects") && "cursor-not-allowed opacity-50",
                )}
              >
                <FeatureIcon icon={createProjects.icon} on={value.features.includes("create-projects")} />
                <span className="min-w-0 flex-1">
                  <span className="block text-sm font-medium">{createProjects.label}</span>
                  <span className="block text-xs text-muted-foreground">{createProjects.description}</span>
                </span>
                <Switch
                  checked={value.features.includes("create-projects")}
                  disabled={!has("create-projects") && !value.features.includes("create-projects")}
                  onCheckedChange={(on) => setFeature("create-projects", on)}
                />
              </label>
            </Tip>
            <div className="grid gap-2 sm:grid-cols-2">
              {seeable.map((f) => {
                const on = value.features.includes(f);
                const off = !has(f) && !on;
                const l = featureLabels[f];
                return (
                  <Tip key={f} content={off && notYours}>
                    <button
                      type="button"
                      aria-pressed={on}
                      aria-disabled={off}
                      onClick={() => !off && setFeature(f, !on)}
                      className={cn(
                        "flex items-center gap-3 rounded-lg border px-3 py-2 text-left transition-colors outline-none hover:bg-muted/50 focus-visible:ring-3 focus-visible:ring-ring/50",
                        on && "border-primary/50 bg-primary/5 hover:bg-primary/10",
                        off && "cursor-not-allowed opacity-50 hover:bg-transparent",
                      )}
                    >
                      <FeatureIcon icon={l.icon} on={on} />
                      <span className="min-w-0 flex-1">
                        <span className="block truncate text-sm font-medium">{l.label}</span>
                        <span className="block truncate text-xs text-muted-foreground">{l.description}</span>
                      </span>
                      <Check className={cn("size-4 shrink-0 text-primary", !on && "invisible")} />
                    </button>
                  </Tip>
                );
              })}
            </div>
          </section>
        </>
      )}
    </div>
  );
}

function RoleCard({
  icon: Icon,
  title,
  description,
  selected,
  onSelect,
}: {
  icon: LucideIcon;
  title: string;
  description: string;
  selected: boolean;
  onSelect: () => void;
}) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={selected}
      onClick={onSelect}
      className={cn(
        "flex items-start gap-3 rounded-lg border p-3 text-left transition-colors outline-none hover:bg-muted/50 focus-visible:ring-3 focus-visible:ring-ring/50",
        selected && "border-primary ring-1 ring-primary hover:bg-transparent",
      )}
    >
      <FeatureIcon icon={Icon} on={selected} />
      <span className="min-w-0">
        <span className="block text-sm font-medium">{title}</span>
        <span className="block text-xs text-muted-foreground">{description}</span>
      </span>
    </button>
  );
}

function FeatureIcon({ icon: Icon, on }: { icon: LucideIcon; on: boolean }) {
  return (
    <span
      className={cn(
        "flex size-8 shrink-0 items-center justify-center rounded-md transition-colors",
        on ? "bg-primary/10 text-primary" : "bg-muted text-muted-foreground",
      )}
    >
      <Icon className="size-4" />
    </span>
  );
}

function PermChip({ perm, on, disabled, onToggle }: { perm: Perm; on: boolean; disabled?: boolean; onToggle: () => void }) {
  const l = permLabels[perm];
  return (
    <Tip content={disabled ? notYours : l.description}>
      <button
        type="button"
        aria-pressed={on}
        aria-disabled={disabled}
        onClick={() => !disabled && onToggle()}
        className={cn(
          "inline-flex h-7 items-center gap-1.5 rounded-md border bg-background px-2 text-xs font-medium text-muted-foreground transition-colors outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50",
          on && "border-primary/50 bg-primary/10 text-primary hover:text-primary",
          perm === "delete" && on && "border-destructive/50 bg-destructive/10 text-destructive hover:text-destructive",
          disabled && "cursor-not-allowed opacity-50 hover:text-muted-foreground",
        )}
      >
        <l.icon className="size-3.5" />
        {l.label}
      </button>
    </Tip>
  );
}
