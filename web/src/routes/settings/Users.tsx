import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Mail, Pencil, Trash2, TriangleAlert, UserPlus, UserRound } from "lucide-react";
import { useState, type ReactNode } from "react";
import { Link } from "react-router";
import { AccessEditor } from "@/components/access-editor";
import { CopyField, ErrorText, Section, Tag } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { Tip } from "@/components/ui/tooltip";
import { timeAgo } from "@/lib/format";
import { api, type UserAccess, type UserView } from "@/api";
import { SettingsPage } from "./page";

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
                    {i.email ? `Sent to ${i.email} ${timeAgo(i.createdAt)}` : `Created ${timeAgo(i.createdAt)}`}, expires {timeAgo(i.expiresAt)}
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
  const [email, setEmail] = useState("");
  const smtp = useQuery({ queryKey: ["email"], queryFn: api.email });
  const page = `${window.location.origin}${import.meta.env.BASE_URL.replace(/\/$/, "")}${invitePath}`;
  const create = useMutation({
    meta: { error: "Couldn't create the invite" },
    mutationFn: () => api.createInvite({ ...access, ...(email.trim() && { email: email.trim(), link: page }) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["invites"] }),
  });
  if (create.data) {
    // The token stays in the fragment: it never reaches a server log.
    const link = `${page}#${create.data.token}`;
    const sent = !!create.data.email && !create.data.emailError;
    return (
      <>
        <DialogHeader>
          <DialogTitle>{sent ? "Invite sent" : "Invite link created"}</DialogTitle>
          <DialogDescription className="flex items-center gap-1.5">
            {sent ? (
              <>
                <Mail className="size-4 shrink-0" />
                Emailed to {create.data.email}. The link also works copied from here, once, and won't be shown again.
              </>
            ) : (
              <>
                <TriangleAlert className="size-4 shrink-0 text-amber-500" />
                Copy it now: it won't be shown again. Whoever opens it first gets the account.
              </>
            )}
          </DialogDescription>
        </DialogHeader>
        {create.data.emailError && (
          <p className="text-sm text-destructive">Couldn't email it: {create.data.emailError}. Send the link yourself.</p>
        )}
        <CopyField value={link} />
        <DialogFooter>
          <Button onClick={onDone}>Done</Button>
        </DialogFooter>
      </>
    );
  }
  const noSmtp = smtp.data && smtp.data.providers.length === 0;
  const via = smtp.data?.providers.find((p) => p.id === smtp.data?.invites)?.name;
  return (
    <>
      <DialogHeader>
        <DialogTitle>Invite a user</DialogTitle>
        <DialogDescription>They pick their username and password from the link, valid 7 days.</DialogDescription>
      </DialogHeader>
      <AccessEditor value={access} onChange={setAccess} />
      <FloatingInput
        label="Email (optional)"
        type="email"
        autoComplete="off"
        value={email}
        disabled={noSmtp}
        onChange={(e) => setEmail(e.target.value)}
        placeholder="new.user@example.com"
        description={
          noSmtp ? (
            <>
              Add an email provider in{" "}
              <Link to="/email" className="underline">
                Settings › Email
              </Link>{" "}
              to email invites. You can still copy the link.
            </>
          ) : (
            `Sends them the link${via ? ` through ${via}` : ""}. Leave empty to copy it yourself.`
          )
        }
      />
      <DialogFooter>
        <Button loading={create.isPending} onClick={() => create.mutate()}>
          {email.trim() ? "Send invite" : "Create link"}
        </Button>
      </DialogFooter>
    </>
  );
}
