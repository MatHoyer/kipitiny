import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Eye, KeySquare, Plus, Rocket, ShieldCheck, Trash2, TriangleAlert, type LucideIcon } from "lucide-react";
import { useState, type FormEvent } from "react";
import { CopyField, EmptyState, ErrorText, Section, Tag } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingSelect } from "@/components/ui/floating-select";
import { timeAgo } from "@/lib/format";
import { api, type Scope } from "@/api";
import { SettingsPage } from "./page";

const scopes: Record<Scope, { icon: LucideIcon; label: string; description: string }> = {
  read: { icon: Eye, label: "Read", description: "Status, logs, backups list" },
  deploy: { icon: Rocket, label: "Deploy", description: "Read + deploy, rollback, start/stop, back up" },
  admin: { icon: ShieldCheck, label: "Admin", description: "Everything, including settings, secrets and restores" },
};

const mcpCommand = (token: string) =>
  `claude mcp add --transport http kipitiny ${window.location.origin}/mcp --header "Authorization: Bearer ${token}"`;

export function TokensPage() {
  const qc = useQueryClient();
  const tokens = useQuery({ queryKey: ["tokens"], queryFn: api.tokens });
  // The dialog lives here, not in the empty state: creating the first token
  // replaces the empty state, and the new token must stay on screen.
  const [creating, setCreating] = useState(false);
  const remove = useMutation({
    meta: { error: "Couldn't revoke the token" },
    mutationFn: api.deleteToken,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["tokens"] }),
  });
  const add = (
    <Button size="sm" onClick={() => setCreating(true)}>
      <Plus data-icon="inline-start" />
      Create token
    </Button>
  );

  return (
    <SettingsPage actions={!!tokens.data?.length && add}>
      <ErrorText error={tokens.error} />
      <Section title="MCP endpoint" description="Where AI agents connect, with a token of the scope they need.">
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
            const s = scopes[t.scope];
            return (
              <Card key={t.id} className="gap-3 px-4">
                <div className="flex items-start gap-3">
                  <span className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-muted [&>svg]:size-5">
                    <s.icon />
                  </span>
                  <div className="min-w-0 flex-1">
                    <p className="truncate font-medium">{t.name}</p>
                    <p className="truncate text-xs text-muted-foreground">{s.description}</p>
                  </div>
                  <Tag>{s.label}</Tag>
                </div>
                <p className="text-xs text-muted-foreground">
                  Created {timeAgo(t.createdAt)} · {t.lastUsedAt ? `last used ${timeAgo(t.lastUsedAt)}` : "never used"}
                </p>
                <div className="mt-auto flex justify-end">
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
      <Dialog open={creating} onOpenChange={setCreating}>
        <DialogContent className="sm:max-w-lg">
          {/* Remounted on every open, so a previous token never shows again. */}
          {creating && <TokenForm onDone={() => setCreating(false)} />}
        </DialogContent>
      </Dialog>
    </SettingsPage>
  );
}

function TokenForm({ onDone }: { onDone: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [scope, setScope] = useState<Scope>("read");
  const create = useMutation({
    meta: { error: "Couldn't create the token" },
    mutationFn: () => api.createToken(name.trim(), scope),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["tokens"] }),
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    create.mutate();
  };

  if (create.data) {
    return (
      <>
        <DialogHeader>
          <DialogTitle>Token {create.data.name} created</DialogTitle>
          <DialogDescription className="flex items-center gap-1.5">
            <TriangleAlert className="size-4 shrink-0 text-amber-500" />
            Copy it now: it won't be shown again.
          </DialogDescription>
        </DialogHeader>
        <CopyField value={create.data.token} />
        <p className="text-sm text-muted-foreground">Connect Claude Code:</p>
        <CopyField value={mcpCommand(create.data.token)} />
        <DialogFooter>
          <Button onClick={onDone}>Done</Button>
        </DialogFooter>
      </>
    );
  }
  return (
    <form onSubmit={onSubmit} className="contents">
      <DialogHeader>
        <DialogTitle>New API token</DialogTitle>
        <DialogDescription>For CI, a script or an AI agent.</DialogDescription>
      </DialogHeader>
      <div className="space-y-4">
        <FloatingInput label="Name" required autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="github-ci" />
        <FloatingSelect
          label="Scope"
          value={scope}
          onValueChange={(v) => setScope(v as Scope)}
          options={(Object.keys(scopes) as Scope[]).map((v) => ({ value: v, label: `${scopes[v].label}: ${scopes[v].description}` }))}
        />
      </div>
      <DialogFooter>
        <Button type="submit" loading={create.isPending}>
          Create token
        </Button>
      </DialogFooter>
    </form>
  );
}
