import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Mail, Pencil, ShieldCheck, Trash2, TriangleAlert, UserPlus, UserRound, Users as UsersIcon } from "lucide-react";
import { useState } from "react";
import { CheckboxField, CopyField, EmptyState, ErrorText, Section, Tag } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Segmented, SegmentedItem } from "@/components/ui/segmented";
import { Tip } from "@/components/ui/tooltip";
import { timeAgo } from "@/lib/format";
import { api, features, perms, type Feature, type Perm, type UserAccess, type UserView } from "@/api";
import { SettingsPage } from "./page";

export const permLabels: Record<Perm, { label: string; description: string }> = {
  view: { label: "View", description: "Services, logs, stats, deployments and backups" },
  deploy: { label: "Deploy", description: "Deploy, roll back, start, stop, maintenance" },
  settings: { label: "Settings", description: "Create and edit services, compose files, networks, uptime checks" },
  env: { label: "Env", description: "Variables, secrets and connection strings" },
  domains: { label: "Domains", description: "Public domains and their middlewares" },
  backups: { label: "Backups", description: "Back up now, verify, schedules" },
  restore: { label: "Restore", description: "Replace data with a backup" },
  download: { label: "Download", description: "Backup files and exports with secrets" },
  data: { label: "Data", description: "Browse databases, read-only console" },
  "data-write": { label: "Data write", description: "Edit rows, schema and run writing queries" },
  files: { label: "Files", description: "Browse volume files" },
  "files-write": { label: "Files write", description: "Upload, edit, move and delete volume files" },
  terminal: { label: "Terminal", description: "A shell in the containers" },
  git: { label: "Git", description: "Link, sync and unlink a repository" },
  delete: { label: "Delete", description: "Delete services and the project" },
};

const featureLabels: Record<Feature, { label: string; description: string }> = {
  "create-projects": { label: "Create projects", description: "With every permission on the ones they create" },
  storage: { label: "Storage", description: "Pick where backups go" },
  registries: { label: "Registries", description: "See which registries pulls can use" },
  "git-providers": { label: "Git providers", description: "Pick a provider when linking a repository" },
  "secret-providers": { label: "Password managers", description: "Reference their items in env" },
  servers: { label: "Servers", description: "See the servers, without how to reach them" },
  networks: { label: "Networks", description: "See the networks created by hand" },
  notifications: { label: "Notifications", description: "See the channels, without where they send" },
  domains: { label: "Domains", description: "See the base domains" },
};

const memberDefault: UserAccess = { role: "member", features: [], grants: {} };

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

  return (
    <SettingsPage
      actions={
        <Button size="sm" onClick={() => setInviting(true)}>
          <UserPlus data-icon="inline-start" />
          Invite user
        </Button>
      }
    >
      <ErrorText error={users.error ?? invites.error} />
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {users.data?.map((u) => {
          const projects = Object.keys(u.grants).length;
          const me = u.username === account.data?.username;
          return (
            <Card key={u.id} className="gap-3 px-4">
              <div className="flex items-start gap-3">
                <span className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-muted text-sm font-semibold uppercase">
                  {u.username.slice(0, 1)}
                </span>
                <div className="min-w-0 flex-1">
                  <p className="truncate font-medium">
                    {u.username}
                    {me && <span className="font-normal text-muted-foreground"> (you)</span>}
                  </p>
                  <p className="truncate text-xs text-muted-foreground">
                    {u.role === "admin" ? "Everything" : `${projects} project${projects === 1 ? "" : "s"}, ${u.features.length} feature${u.features.length === 1 ? "" : "s"}`}
                  </p>
                </div>
                <Tag>{u.role === "admin" ? "Admin" : "Member"}</Tag>
              </div>
              <p className="text-xs text-muted-foreground">
                Joined {timeAgo(u.createdAt)} · {u.totpEnabled ? "2FA on" : "2FA off"}
              </p>
              <div className="mt-auto flex justify-end gap-1">
                <Tip content="Edit access">
                  <Button variant="ghost" size="icon-sm" aria-label="Edit access" onClick={() => setEditing(u)}>
                    <Pencil />
                  </Button>
                </Tip>
                {!me && (
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
                )}
              </div>
            </Card>
          );
        })}
      </div>
      <Section title="Pending invites" description="Each link creates one account, within 7 days.">
        {invites.data?.length === 0 ? (
          <EmptyState icon={Mail} title="No pending invites" />
        ) : (
          <ul className="-mx-3 -my-2 divide-y">
            {invites.data?.map((i) => (
              <li key={i.id} className="flex items-center gap-3 px-3 py-2 text-sm">
                {i.role === "admin" ? <ShieldCheck className="size-4 shrink-0" /> : <UserRound className="size-4 shrink-0" />}
                <span className="min-w-0 flex-1 truncate">
                  {i.role === "admin" ? "Admin" : `Member of ${Object.keys(i.grants).length} project(s)`}
                  <span className="text-muted-foreground"> · created {timeAgo(i.createdAt)}, expires {timeAgo(i.expiresAt)}</span>
                </span>
                <Button variant="ghost" size="icon-sm" title="Revoke" aria-label="Revoke" onClick={() => revoke.mutate(i.id)}>
                  <Trash2 />
                </Button>
              </li>
            ))}
          </ul>
        )}
      </Section>
      {users.data?.length === 1 && (
        <EmptyState
          icon={UsersIcon}
          title="Only you so far"
          description="Invite people as admins, or as members who only see and do what you grant them, project by project."
        />
      )}
      <Dialog open={inviting} onOpenChange={setInviting}>
        <DialogContent className="sm:max-w-3xl">{inviting && <InviteForm onDone={() => setInviting(false)} />}</DialogContent>
      </Dialog>
      <Dialog open={!!editing} onOpenChange={(open) => !open && setEditing(null)}>
        <DialogContent className="sm:max-w-3xl">{editing && <EditForm user={editing} onDone={() => setEditing(null)} />}</DialogContent>
      </Dialog>
    </SettingsPage>
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

/** A role, and for a member the server features and the permission matrix, project by permission. */
function AccessEditor({ value, onChange }: { value: UserAccess; onChange: (a: UserAccess) => void }) {
  const projects = useQuery({ queryKey: ["projects"], queryFn: api.projects });
  const setPerms = (projectId: string, ps: Perm[]) => {
    const grants = { ...value.grants };
    if (ps.length) grants[projectId] = perms.filter((p) => ps.includes(p));
    else delete grants[projectId];
    onChange({ ...value, grants });
  };
  const toggle = (projectId: string, p: Perm, on: boolean) => {
    const cur = value.grants[projectId] ?? [];
    let next = on ? [...cur, p] : cur.filter((q) => q !== p);
    if (on && p !== "view" && !next.includes("view")) next = ["view", ...next];
    if (!on && p === "view") next = []; // every permission implies view
    setPerms(projectId, next);
  };

  return (
    <div className="max-h-[65vh] space-y-5 overflow-y-auto">
      <Segmented value={value.role} onValueChange={(v) => onChange({ ...value, role: v as UserAccess["role"] })} aria-label="Role">
        <SegmentedItem value="member">Member</SegmentedItem>
        <SegmentedItem value="admin">Admin</SegmentedItem>
      </Segmented>
      {value.role === "admin" ? (
        <p className="text-sm text-muted-foreground">Admins do everything: every project, server settings, users and the audit log.</p>
      ) : (
        <>
          <div className="space-y-3">
            <p className="text-sm font-medium">Server</p>
            <div className="grid gap-3 sm:grid-cols-2">
              {features.map((f) => (
                <CheckboxField
                  key={f}
                  label={featureLabels[f].label}
                  description={featureLabels[f].description}
                  checked={value.features.includes(f)}
                  onCheckedChange={(on) =>
                    onChange({ ...value, features: on ? [...value.features, f] : value.features.filter((x) => x !== f) })
                  }
                />
              ))}
            </div>
          </div>
          <div className="space-y-3">
            <p className="text-sm font-medium">Projects</p>
            {projects.data?.length === 0 ? (
              <p className="text-sm text-muted-foreground">No projects yet.</p>
            ) : (
              <div className="overflow-x-auto rounded-lg border">
                <table className="w-full text-xs">
                  <thead>
                    <tr className="border-b bg-muted/50">
                      <th className="px-3 py-2 text-left font-medium">Project</th>
                      <th className="px-1 py-2 font-medium">All</th>
                      {perms.map((p) => (
                        <th key={p} className="px-1 py-2 font-medium whitespace-nowrap">
                          <Tip content={permLabels[p].description}>
                            <span className="cursor-help">{permLabels[p].label}</span>
                          </Tip>
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {projects.data?.map((pr) => {
                      const cur = value.grants[pr.id] ?? [];
                      const all = cur.length === perms.length;
                      return (
                        <tr key={pr.id} className="border-b last:border-0">
                          <td className="px-3 py-2 font-medium whitespace-nowrap">{pr.name}</td>
                          <td className="px-1 py-2 text-center">
                            <Checkbox
                              aria-label={`Every permission on ${pr.name}`}
                              checked={all ? true : cur.length ? "indeterminate" : false}
                              onCheckedChange={(v) => setPerms(pr.id, v === true ? [...perms] : [])}
                            />
                          </td>
                          {perms.map((p) => (
                            <td key={p} className="px-1 py-2 text-center">
                              <Checkbox
                                aria-label={`${permLabels[p].label} on ${pr.name}`}
                                checked={cur.includes(p)}
                                onCheckedChange={(v) => toggle(pr.id, p, v === true)}
                              />
                            </td>
                          ))}
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </>
      )}
    </div>
  );
}
