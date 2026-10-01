import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Trash2 } from "lucide-react";
import { useState, type FormEvent } from "react";
import { CopyField, Empty, Section, Tag } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingSelect } from "@/components/ui/floating-select";
import { timeAgo } from "@/lib/format";
import { api, type Scope } from "@/api";
import { SettingsPage } from "./page";

const scopes: [Scope, string][] = [
  ["read", "Read: status, logs, backups list"],
  ["deploy", "Deploy: read + deploy, rollback, start/stop, back up"],
  ["admin", "Admin: everything, including settings, secrets and restores"],
];

export function TokensPage() {
  return (
    <SettingsPage actions={<TokenDialog />}>
      <Mcp />
      <Tokens />
    </SettingsPage>
  );
}

/** Where AI agents connect, ready to paste. */
function Mcp() {
  const url = `${window.location.origin}/mcp`;
  return (
    <Section title="MCP endpoint">
      <CopyField value={url} />
      <p className="text-sm text-muted-foreground">Connect Claude Code:</p>
      <CopyField value={`claude mcp add --transport http kipitiny ${url} --header "Authorization: Bearer <token>"`} />
    </Section>
  );
}

function Tokens() {
  const qc = useQueryClient();
  const tokens = useQuery({ queryKey: ["tokens"], queryFn: api.tokens });
  const remove = useMutation({
    meta: { error: "Couldn't revoke the token" },
    mutationFn: api.deleteToken,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["tokens"] }),
  });

  return (
    <Section>
      {tokens.data?.length === 0 ? (
        <Empty>No tokens yet.</Empty>
      ) : (
        <ul className="-my-2 divide-y">
          {tokens.data?.map((t) => (
            <li key={t.id} className="flex items-center justify-between gap-4 py-2.5">
              <div>
                <p className="flex items-center gap-2 text-sm font-medium">
                  {t.name}
                  <Tag>{t.scope}</Tag>
                </p>
                <p className="text-xs text-muted-foreground">
                  created {timeAgo(t.createdAt)} · {t.lastUsedAt ? `last used ${timeAgo(t.lastUsedAt)}` : "never used"}
                </p>
              </div>
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
            </li>
          ))}
        </ul>
      )}
    </Section>
  );
}

function TokenDialog() {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [scope, setScope] = useState<Scope>("read");
  const [created, setCreated] = useState<string | null>(null);
  const create = useMutation({
    meta: { error: "Couldn't create the token" },
    mutationFn: () => api.createToken(name.trim(), scope),
    onSuccess: (t) => {
      setCreated(t.token);
      qc.invalidateQueries({ queryKey: ["tokens"] });
    },
  });
  const onOpenChange = (next: boolean) => {
    setOpen(next);
    if (!next) {
      setName("");
      setScope("read");
      setCreated(null);
      create.reset();
    }
  };
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    create.mutate();
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button size="sm">
          <Plus data-icon="inline-start" />
          Create token
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-lg">
        {created ? (
          <>
            <DialogHeader>
              <DialogTitle>Token created</DialogTitle>
              <DialogDescription>Copy it now: it won't be shown again.</DialogDescription>
            </DialogHeader>
            <CopyField value={created} />
            <p className="text-sm text-muted-foreground">Connect Claude Code:</p>
            <CopyField
              value={`claude mcp add --transport http kipitiny ${window.location.origin}/mcp --header "Authorization: Bearer ${created}"`}
            />
            <DialogFooter>
              <Button onClick={() => onOpenChange(false)}>Done</Button>
            </DialogFooter>
          </>
        ) : (
          <form onSubmit={onSubmit} className="contents">
            <DialogHeader>
              <DialogTitle>New API token</DialogTitle>
              <DialogDescription>For a script or an AI agent.</DialogDescription>
            </DialogHeader>
            <div className="space-y-4">
              <FloatingInput label="Name" required autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="claude-code" />
              <FloatingSelect
                label="Scope"
                value={scope}
                onValueChange={(v) => setScope(v as Scope)}
                options={scopes.map(([value, label]) => ({ value, label }))}
              />
            </div>
            <DialogFooter>
              <Button type="submit" disabled={create.isPending}>
                Create token
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
