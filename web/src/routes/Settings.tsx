import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Trash2 } from "lucide-react";
import { useState, type FormEvent } from "react";
import { CopyField, Empty, ErrorText, Mono, Section, Tag } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { PageBody, PageHeader } from "@/components/page-header";
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
import { cn } from "@/lib/utils";
import { api, type Scope } from "../api";
import { Servers } from "./Servers";

const scopes: [Scope, string][] = [
  ["read", "Read: status, logs, backups list"],
  ["deploy", "Deploy: read + deploy, rollback, start/stop, back up"],
  ["admin", "Admin: everything, including settings, secrets and restores"],
];

export function Settings() {
  return (
    <>
      <PageHeader crumbs={[{ label: "Settings" }]} />
      <PageBody>
        <Servers />
        <Tokens />
        <Audit />
      </PageBody>
    </>
  );
}

function Tokens() {
  const qc = useQueryClient();
  const tokens = useQuery({ queryKey: ["tokens"], queryFn: api.tokens });
  const remove = useMutation({
    mutationFn: api.deleteToken,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["tokens"] }),
  });

  return (
    <Section
      title="API tokens & MCP"
      description={
        <>
          Tokens authenticate scripts (<Mono>Authorization: Bearer …</Mono> on <Mono>/api</Mono>) and AI agents on the
          MCP endpoint <Mono>{window.location.origin}/mcp</Mono>. Every change they make is in the audit log.
        </>
      }
      actions={<TokenDialog />}
    >
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
      <ErrorText error={remove.error} />
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
        <Button variant="outline" size="sm">
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
              <ErrorText error={create.error} />
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

function Audit() {
  const audit = useQuery({ queryKey: ["audit"], queryFn: api.audit, refetchInterval: 15_000 });
  const th = "px-2 pb-2 font-medium";
  return (
    <Section title="Audit log" description="Every change, by whom, and how it went.">
      <ErrorText error={audit.error} />
      {audit.data?.length === 0 ? (
        <Empty>Nothing yet.</Empty>
      ) : (
        <div className="-mx-2 max-h-96 overflow-auto">
          <table className="w-full text-left text-xs">
            <thead className="sticky top-0 bg-card text-muted-foreground">
              <tr className="border-b">
                <th className={th}>When</th>
                <th className={th}>Who</th>
                <th className={th}>Action</th>
                <th className={th}>Result</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {audit.data?.map((e) => (
                <tr key={e.id} className="hover:bg-muted/50">
                  <td className="px-2 py-1.5 whitespace-nowrap text-muted-foreground" title={new Date(e.createdAt).toLocaleString()}>
                    {timeAgo(e.createdAt)}
                  </td>
                  <td className="px-2 py-1.5">{e.actor}</td>
                  <td className="px-2 py-1.5 font-mono">
                    {e.action}
                    {e.target && <span className="text-muted-foreground"> {e.target}</span>}
                  </td>
                  <td className={cn("px-2 py-1.5", e.status >= 400 ? "text-destructive" : "text-muted-foreground")} title={e.error}>
                    {e.status}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Section>
  );
}
