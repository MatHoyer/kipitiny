import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
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
  Mail,
  Network,
  Package,
  Pencil,
  Rocket,
  Server,
  Settings2,
  ShieldCheck,
  SquareTerminal,
  Trash2,
  TriangleAlert,
  UserPlus,
  UserRound,
  Variable,
  type LucideIcon,
} from "lucide-react";
import { useState, type ReactNode } from "react";
import { CopyField, ErrorText, Section, Tag } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Segmented, SegmentedItem } from "@/components/ui/segmented";
import { Switch } from "@/components/ui/switch";
import { Tip } from "@/components/ui/tooltip";
import { timeAgo } from "@/lib/format";
import { cn } from "@/lib/utils";
import { api, features, perms, type Feature, type Perm, type UserAccess, type UserView } from "@/api";
import { SettingsPage } from "./page";

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

const memberDefault: UserAccess = { role: "member", features: [], grants: {} };

const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? "" : "s"}`;

export function UsersPage() {
  const qc = useQueryClient();
  const users = useQuery({ queryKey: ["users"], queryFn: api.users });
  const invites = useQuery({ queryKey: ["invites"], queryFn: api.invites });
  const account = useQuery({ queryKey: ["account"], queryFn: api.account });
  const [inviting, setInviting] = useState(false);
  const [editing, setEditing] = useState<UserView | null>(null);
  const remove = useMutation({
    meta: { error: "Couldn't remove the user" },
    mutationFn: api.deleteUser,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["users"] }),
  });
  const revoke = useMutation({
    meta: { error: "Couldn't revoke the invite" },
    mutationFn: api.deleteInvite,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["invites"] }),
  });
  const invite = (
    <Button size="sm" onClick={() => setInviting(true)}>
      <UserPlus data-icon="inline-start" />
      Invite user
    </Button>
  );
  const admins = users.data?.filter((u) => u.role === "admin") ?? [];
  const members = users.data?.filter((u) => u.role !== "admin") ?? [];
  const row = (u: UserView) => {
    const me = u.username === account.data?.username;
    return (
      <UserRow
        key={u.id}
        user={u}
        me={me}
        onEdit={() => setEditing(u)}
        remove={
          !me && (
            <ConfirmDialog
              trigger={
                <Button variant="ghost" size="icon-sm" title="Remove" aria-label="Remove" className="text-muted-foreground hover:text-destructive">
                  <Trash2 />
                </Button>
              }
              title={`Remove ${u.username}?`}
              description="They're signed out and their API tokens stop working. Repositories they linked stop syncing until linked again."
              confirmLabel="Remove"
              onConfirm={() => remove.mutate(u.id)}
            />
          )
        }
      />
    );
  };

  return (
    <SettingsPage actions={invite}>
      <ErrorText error={users.error ?? invites.error} />
      <Section title="Admins" description="Every project, server settings, users and the audit log.">
        <ul className="-mx-3 -my-2 divide-y">{admins.map(row)}</ul>
      </Section>
      <Section title="Members" description="Only see and do what you grant them, project by project.">
        {users.data && members.length === 0 ? (
          <div className="flex flex-col items-center gap-3 rounded-lg border border-dashed py-8 text-center">
            <UserRound className="size-5 text-muted-foreground" />
            <p className="text-sm text-muted-foreground">No members yet. Invite someone with access to the projects you pick.</p>
          </div>
        ) : (
          <ul className="-mx-3 -my-2 divide-y">{members.map(row)}</ul>
        )}
      </Section>
      {!!invites.data?.length && (
        <Section title="Pending invites" description="Each link creates one account, within 7 days.">
          <ul className="-mx-3 -my-2 divide-y">
            {invites.data.map((i) => (
              <li key={i.id} className="flex items-center gap-3 px-3 py-2.5 text-sm">
                <span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-muted">
                  <Mail className="size-4" />
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate">
                    {i.role === "admin" ? "Admin" : `Member of ${plural(Object.keys(i.grants).length, "project")}`}
                  </span>
                  <span className="block truncate text-xs text-muted-foreground">
                    Created {timeAgo(i.createdAt)}, expires {timeAgo(i.expiresAt)}
                  </span>
                </span>
                <Button variant="ghost" size="icon-sm" title="Revoke" aria-label="Revoke" onClick={() => revoke.mutate(i.id)}>
                  <Trash2 />
                </Button>
              </li>
            ))}
          </ul>
        </Section>
      )}
      <Dialog open={inviting} onOpenChange={setInviting}>
        <DialogContent className="sm:max-w-2xl">{inviting && <InviteForm onDone={() => setInviting(false)} />}</DialogContent>
      </Dialog>
      <Dialog open={!!editing} onOpenChange={(open) => !open && setEditing(null)}>
        <DialogContent className="sm:max-w-2xl">{editing && <EditForm user={editing} onDone={() => setEditing(null)} />}</DialogContent>
      </Dialog>
    </SettingsPage>
  );
}

function UserRow({ user: u, me, onEdit, remove }: { user: UserView; me: boolean; onEdit: () => void; remove: ReactNode }) {
  const projects = Object.keys(u.grants).length;
  return (
    <li className="flex items-center gap-3 px-3 py-2.5">
      <span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-muted text-sm font-semibold uppercase">
        {u.username.slice(0, 1)}
      </span>
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm font-medium">
          {u.username}
          {me && <span className="font-normal text-muted-foreground"> (you)</span>}
        </p>
        <p className="truncate text-xs text-muted-foreground">
          {u.role !== "admin" && `${plural(projects, "project")}, ${plural(u.features.length, "server feature")} · `}
          Joined {timeAgo(u.createdAt)}
        </p>
      </div>
      {u.totpEnabled ? <Tag className="hidden sm:inline">2FA</Tag> : <Tag className="hidden bg-transparent sm:inline">No 2FA</Tag>}
      <div className="flex gap-1">
        <Tip content="Edit access">
          <Button variant="ghost" size="icon-sm" aria-label="Edit access" onClick={onEdit}>
            <Pencil />
          </Button>
        </Tip>
        {remove || <span className="size-7" />}
      </div>
    </li>
  );
}

function EditForm({ user, onDone }: { user: UserView; onDone: () => void }) {
  const qc = useQueryClient();
  const [access, setAccess] = useState<UserAccess>({ role: user.role, features: user.features, grants: user.grants });
  const save = useMutation({
    meta: { error: "Couldn't change the access" },
    mutationFn: () => api.setUserAccess(user.id, access),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["users"] });
      qc.invalidateQueries({ queryKey: ["account"] });
      onDone();
    },
  });
  return (
    <>
      <DialogHeader>
        <DialogTitle>Access of {user.username}</DialogTitle>
        <DialogDescription>Changes apply to their next request, API tokens included.</DialogDescription>
      </DialogHeader>
      <AccessEditor value={access} onChange={setAccess} />
      <DialogFooter>
        <Button loading={save.isPending} onClick={() => save.mutate()}>
          Save
        </Button>
      </DialogFooter>
    </>
  );
}

/** The page answering invite links; see AcceptInvite. */
export const invitePath = "/invite";

function InviteForm({ onDone }: { onDone: () => void }) {
  const qc = useQueryClient();
  const [access, setAccess] = useState<UserAccess>(memberDefault);
  const create = useMutation({
    meta: { error: "Couldn't create the invite" },
    mutationFn: () => api.createInvite(access),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["invites"] }),
  });
  if (create.data) {
    // The token stays in the fragment: it never reaches a server log.
    const link = `${window.location.origin}${import.meta.env.BASE_URL.replace(/\/$/, "")}${invitePath}#${create.data.token}`;
    return (
      <>
        <DialogHeader>
          <DialogTitle>Invite link created</DialogTitle>
          <DialogDescription className="flex items-center gap-1.5">
            <TriangleAlert className="size-4 shrink-0 text-amber-500" />
            Copy it now: it won't be shown again. Whoever opens it first gets the account.
          </DialogDescription>
        </DialogHeader>
        <CopyField value={link} />
        <DialogFooter>
          <Button onClick={onDone}>Done</Button>
        </DialogFooter>
      </>
    );
  }
  return (
    <>
      <DialogHeader>
        <DialogTitle>Invite a user</DialogTitle>
        <DialogDescription>They pick their username and password from the link, valid 7 days.</DialogDescription>
      </DialogHeader>
      <AccessEditor value={access} onChange={setAccess} />
      <DialogFooter>
        <Button loading={create.isPending} onClick={() => create.mutate()}>
          Create link
        </Button>
      </DialogFooter>
    </>
  );
}

/** A role, and for a member the server features and, project by project, a preset or custom permissions. */
function AccessEditor({ value, onChange }: { value: UserAccess; onChange: (a: UserAccess) => void }) {
  const projects = useQuery({ queryKey: ["projects"], queryFn: api.projects });
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

  return (
    <div className="-mx-1 max-h-[65vh] space-y-6 overflow-y-auto px-1 pb-1">
      <div role="radiogroup" aria-label="Role" className="grid gap-3 sm:grid-cols-2">
        <RoleCard
          icon={UserRound}
          title="Member"
          description="Sees and does only what you grant below."
          selected={value.role === "member"}
          onSelect={() => onChange({ ...value, role: "member" })}
        />
        <RoleCard
          icon={ShieldCheck}
          title="Admin"
          description="Everything: every project, server settings, users, audit log."
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
                            <SegmentedItem key={p.value} value={p.value}>
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
                                  <PermChip key={p} perm={p} on={cur.includes(p)} onToggle={() => toggle(pr.id, p)} />
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
              <p className="text-xs text-muted-foreground">What they may see in settings, read-only and without secrets.</p>
            </div>
            <label className="flex cursor-pointer items-center gap-3 rounded-lg border px-3 py-2.5">
              <FeatureIcon icon={featureLabels["create-projects"].icon} on={value.features.includes("create-projects")} />
              <span className="min-w-0 flex-1">
                <span className="block text-sm font-medium">{featureLabels["create-projects"].label}</span>
                <span className="block text-xs text-muted-foreground">{featureLabels["create-projects"].description}</span>
              </span>
              <Switch checked={value.features.includes("create-projects")} onCheckedChange={(on) => setFeature("create-projects", on)} />
            </label>
            <div className="grid gap-2 sm:grid-cols-2">
              {seeable.map((f) => {
                const on = value.features.includes(f);
                const l = featureLabels[f];
                return (
                  <button
                    key={f}
                    type="button"
                    aria-pressed={on}
                    onClick={() => setFeature(f, !on)}
                    className={cn(
                      "flex items-center gap-3 rounded-lg border px-3 py-2 text-left transition-colors outline-none hover:bg-muted/50 focus-visible:ring-3 focus-visible:ring-ring/50",
                      on && "border-primary/50 bg-primary/5 hover:bg-primary/10",
                    )}
                  >
                    <FeatureIcon icon={l.icon} on={on} />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-sm font-medium">{l.label}</span>
                      <span className="block truncate text-xs text-muted-foreground">{l.description}</span>
                    </span>
                    <Check className={cn("size-4 shrink-0 text-primary", !on && "invisible")} />
                  </button>
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

function PermChip({ perm, on, onToggle }: { perm: Perm; on: boolean; onToggle: () => void }) {
  const l = permLabels[perm];
  return (
    <Tip content={l.description}>
      <button
        type="button"
        aria-pressed={on}
        onClick={onToggle}
        className={cn(
          "inline-flex h-7 items-center gap-1.5 rounded-md border bg-background px-2 text-xs font-medium text-muted-foreground transition-colors outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50",
          on && "border-primary/50 bg-primary/10 text-primary hover:text-primary",
          perm === "delete" && on && "border-destructive/50 bg-destructive/10 text-destructive hover:text-destructive",
        )}
      >
        <l.icon className="size-3.5" />
        {l.label}
      </button>
    </Tip>
  );
}
