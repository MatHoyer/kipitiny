import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeySquare, Pencil, Plus, RefreshCw, ShieldCheck, SlidersHorizontal, Trash2, TriangleAlert } from "lucide-react";
import { useState, type FormEvent } from "react";
import { AccessEditor } from "@/components/access-editor";
import { CopyField, EmptyState, ErrorText, Section, Tag } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { useAccess } from "@/lib/access";
import { timeAgo } from "@/lib/format";
import { api, type ApiToken, type UserAccess } from "@/api";
import { SettingsPage } from "./page";

/** A token's access in the editor's terms: "admin" is everything its owner may do, "member" restricted. */
const tokenRoles = {
  admin: { title: "Same as you", description: "Everything you may do, following your access as it changes." },
  member: { title: "Restricted", description: "Only the projects and permissions picked below, never more than yours." },
};

const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? "" : "s"}`;

const mcpCommand = (token: string) =>
  `claude mcp add --transport http kipitiny ${window.location.origin}/mcp --header "Authorization: Bearer ${token}"`;

export function TokensPage() {
  const qc = useQueryClient();
  const tokens = useQuery({ queryKey: ["tokens"], queryFn: api.tokens });
  const { admin } = useAccess();
  // Admins see every user's tokens: name their owner.
  const users = useQuery({ queryKey: ["users"], queryFn: api.users, enabled: admin });
  const owner = (userId?: string) => users.data?.find((u) => u.id === userId)?.username;
  // The dialog lives here, not in the empty state: creating the first token
  // replaces the empty state, and the new token must stay on screen.
  const [editing, setEditing] = useState<ApiToken | "new" | null>(null);
  const remove = useMutation({
    meta: { error: "Couldn't revoke the token" },
    mutationFn: api.deleteToken,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["tokens"] }),
  });
  const roll = useMutation({ meta: { error: "Couldn't roll the token" }, mutationFn: api.rollToken });
  const add = (
    <Button size="sm" onClick={() => setEditing("new")}>
      <Plus data-icon="inline-start" />
      Create token
    </Button>
  );

  return (
    <SettingsPage actions={!!tokens.data?.length && add}>
      <ErrorText error={tokens.error} />
      <Section
        title="MCP endpoint"
        description={
          admin
            ? "Where AI agents connect, with a token restricted to what they need."
            : "Where AI agents connect. A token never does more than you may."
        }
      >
        <CopyField value={`${window.location.origin}/mcp`} />
        <CopyField value={mcpCommand("<token>")} />
      </Section>
      {tokens.data?.length === 0 ? (
        <EmptyState
          icon={KeySquare}
          title="No API tokens yet"
          description="A token lets CI, a script or an AI agent use the API and the MCP endpoint. Every change it makes is in the audit log."
          action={add}
        />
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {tokens.data?.map((t) => {
            const Icon = t.restricted ? SlidersHorizontal : ShieldCheck;
            return (
              <Card key={t.id} className="gap-3 px-4">
                <div className="flex items-start gap-3">
                  <span className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-muted [&>svg]:size-5">
                    <Icon />
                  </span>
                  <div className="min-w-0 flex-1">
                    <p className="truncate font-medium">
                      {t.name}
                      {admin && owner(t.userId) && <span className="font-normal text-muted-foreground"> · {owner(t.userId)}</span>}
                    </p>
                    <p className="truncate text-xs text-muted-foreground">
                      {t.restricted
                        ? `${plural(Object.keys(t.grants ?? {}).length, "project")}, ${plural(t.features?.length ?? 0, "server feature")}`
                        : tokenRoles.admin.description}
                    </p>
                  </div>
                  <Tag>{t.restricted ? tokenRoles.member.title : tokenRoles.admin.title}</Tag>
                </div>
                <p className="text-xs text-muted-foreground">
                  Created {timeAgo(t.createdAt)} · {t.lastUsedAt ? `last used ${timeAgo(t.lastUsedAt)}` : "never used"}
                </p>
                <div className="mt-auto flex justify-end">
                  <Button variant="ghost" size="icon-sm" title="Edit" aria-label="Edit" className="text-muted-foreground" onClick={() => setEditing(t)}>
                    <Pencil />
                  </Button>
                  <ConfirmDialog
                    trigger={
                      <Button variant="ghost" size="icon-sm" title="Roll" aria-label="Roll" className="text-muted-foreground">
                        <RefreshCw />
                      </Button>
                    }
                    title={`Roll token ${t.name}?`}
                    description="It gets a new secret with the same access. Anything using the old one stops working immediately."
                    confirmLabel="Roll"
                    onConfirm={() => roll.mutate(t.id)}
                  />
                  <ConfirmDialog
                    trigger={
                      <Button variant="ghost" size="icon-sm" title="Revoke" aria-label="Revoke" className="text-muted-foreground hover:text-destructive">
                        <Trash2 />
                      </Button>
                    }
                    title={`Revoke token ${t.name}?`}
                    description="Anything using it stops working immediately."
                    confirmLabel="Revoke"
                    onConfirm={() => remove.mutate(t.id)}
                  />
                </div>
              </Card>
            );
          })}
        </div>
      )}
      <Dialog open={!!editing} onOpenChange={(open) => !open && setEditing(null)}>
        <DialogContent className="sm:max-w-2xl">
          {/* Remounted on every open, so a previous token never shows again. */}
          {editing && <TokenForm token={editing === "new" ? undefined : editing} onDone={() => setEditing(null)} />}
        </DialogContent>
      </Dialog>
      <Dialog open={!!roll.data} onOpenChange={(open) => !open && roll.reset()}>
        <DialogContent className="sm:max-w-2xl">
          {roll.data && <TokenSecret title={`Token ${roll.data.name} rolled`} token={roll.data.token} onDone={roll.reset} />}
        </DialogContent>
      </Dialog>
    </SettingsPage>
  );
}

/** Creates a token, or edits token's name and access (its secret stays the same). */
function TokenForm({ token, onDone }: { token?: ApiToken; onDone: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState(token?.name ?? "");
  const [access, setAccess] = useState<UserAccess>({
    role: token?.restricted ? "member" : "admin",
    features: token?.features ?? [],
    grants: token?.grants ?? {},
  });
  const limit = useAccess();
  const save = useMutation({
    meta: { error: token ? "Couldn't save the token" : "Couldn't create the token" },
    mutationFn: async () => {
      const input = { name: name.trim(), restricted: access.role === "member", features: access.features, grants: access.grants };
      if (!token) return api.createToken(input);
      await api.updateToken(token.id, input);
      onDone();
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["tokens"] }),
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  if (save.data) return <TokenSecret title={`Token ${save.data.name} created`} token={save.data.token} onDone={onDone} />;
  return (
    <form onSubmit={onSubmit} className="contents">
      <DialogHeader>
        <DialogTitle>{token ? `Edit token ${token.name}` : "New API token"}</DialogTitle>
        <DialogDescription>{token ? "Anything using it keeps the same token." : "For CI, a script or an AI agent."}</DialogDescription>
      </DialogHeader>
      <div className="space-y-4">
        <FloatingInput label="Name" required autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="github-ci" />
        <AccessEditor value={access} onChange={setAccess} roles={tokenRoles} limit={limit} />
      </div>
      <DialogFooter>
        <Button type="submit" loading={save.isPending}>
          {token ? "Save" : "Create token"}
        </Button>
      </DialogFooter>
    </form>
  );
}

/** A new token's secret, shown once. */
function TokenSecret({ title, token, onDone }: { title: string; token: string; onDone: () => void }) {
  return (
    <>
      <DialogHeader>
        <DialogTitle>{title}</DialogTitle>
        <DialogDescription className="flex items-center gap-1.5">
          <TriangleAlert className="size-4 shrink-0 text-amber-500" />
          Copy it now: it won't be shown again.
        </DialogDescription>
      </DialogHeader>
      <CopyField value={token} />
      <p className="text-sm text-muted-foreground">Connect Claude Code:</p>
      <CopyField value={mcpCommand(token)} />
      <DialogFooter>
        <Button onClick={onDone}>Done</Button>
      </DialogFooter>
    </>
  );
}
