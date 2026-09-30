import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { api, type Scope } from "../api";
import { Button, Card, ErrorText, Field, Input, Select, timeAgo } from "../ui";
import { Servers } from "./Servers";

const scopes: [Scope, string][] = [
  ["read", "Read: status, logs, backups list"],
  ["deploy", "Deploy: read + deploy, rollback, start/stop, back up"],
  ["admin", "Admin: everything, including settings, secrets and restores"],
];

export function Settings() {
  return (
    <div className="space-y-6">
      <h1 className="text-xl font-semibold">Settings</h1>
      <Servers />
      <Tokens />
      <Audit />
    </div>
  );
}

function Tokens() {
  const qc = useQueryClient();
  const tokens = useQuery({ queryKey: ["tokens"], queryFn: api.tokens });
  const [name, setName] = useState("");
  const [scope, setScope] = useState<Scope>("read");
  const [created, setCreated] = useState<string | null>(null);
  const create = useMutation({
    mutationFn: () => api.createToken(name.trim(), scope),
    onSuccess: (t) => {
      setCreated(t.token);
      setName("");
      qc.invalidateQueries({ queryKey: ["tokens"] });
    },
  });
  const remove = useMutation({
    mutationFn: api.deleteToken,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["tokens"] }),
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    create.mutate();
  };
  const mcpCommand = created
    ? `claude mcp add --transport http kipitiny ${window.location.origin}/mcp --header "Authorization: Bearer ${created}"`
    : "";

  return (
    <Card title="API tokens & MCP">
      <div className="space-y-4">
        <p className="text-sm text-zinc-500">
          Tokens authenticate scripts (<span className="font-mono">Authorization: Bearer …</span> on{" "}
          <span className="font-mono">/api</span>) and AI agents on the MCP endpoint{" "}
          <span className="font-mono">{window.location.origin}/mcp</span>. Every change they make is in the audit log.
        </p>
        <form onSubmit={onSubmit} className="grid gap-3 sm:grid-cols-[1fr_1fr_auto] sm:items-end">
          <Field label="Name">
            <Input required value={name} onChange={(e) => setName(e.target.value)} placeholder="claude-code" />
          </Field>
          <Field label="Scope">
            <Select value={scope} onChange={(e) => setScope(e.target.value as Scope)}>
              {scopes.map(([s, label]) => (
                <option key={s} value={s}>
                  {label}
                </option>
              ))}
            </Select>
          </Field>
          <Button disabled={create.isPending}>Create token</Button>
        </form>
        <ErrorText error={create.error ?? remove.error} />
        {created && (
          <div className="space-y-2 rounded-md border border-amber-300 bg-amber-50 p-3 text-sm dark:border-amber-900 dark:bg-amber-950">
            <p className="font-medium">Copy it now: it won't be shown again.</p>
            <CopyLine value={created} />
            <p className="text-xs text-zinc-500">Connect Claude Code:</p>
            <CopyLine value={mcpCommand} />
            <button className="text-xs hover:underline" onClick={() => setCreated(null)}>
              Done
            </button>
          </div>
        )}
        <ul className="divide-y divide-zinc-200 text-sm dark:divide-zinc-800">
          {tokens.data?.map((t) => (
            <li key={t.id} className="flex items-center justify-between gap-4 py-2">
              <div>
                <span className="font-medium">{t.name}</span>{" "}
                <span className="rounded bg-zinc-100 px-1 text-xs dark:bg-zinc-800">{t.scope}</span>
                <p className="text-xs text-zinc-500">
                  created {timeAgo(t.createdAt)} · {t.lastUsedAt ? `last used ${timeAgo(t.lastUsedAt)}` : "never used"}
                </p>
              </div>
              <button
                className="text-xs text-red-600 hover:underline"
                onClick={() => confirm(`Revoke token ${t.name}?`) && remove.mutate(t.id)}
              >
                Revoke
              </button>
            </li>
          ))}
        </ul>
      </div>
    </Card>
  );
}

function CopyLine({ value }: { value: string }) {
  return (
    <div className="flex items-start gap-2">
      <code className="flex-1 rounded bg-white px-2 py-1 font-mono text-xs break-all dark:bg-zinc-900">{value}</code>
      <button className="text-xs hover:underline" onClick={() => navigator.clipboard.writeText(value)}>
        copy
      </button>
    </div>
  );
}

function Audit() {
  const audit = useQuery({ queryKey: ["audit"], queryFn: api.audit, refetchInterval: 15_000 });
  return (
    <Card title="Audit log">
      <ErrorText error={audit.error} />
      {audit.data?.length === 0 ? (
        <p className="text-sm text-zinc-500">Nothing yet.</p>
      ) : (
        <div className="max-h-96 overflow-auto">
          <table className="w-full text-left text-xs">
            <thead className="text-zinc-500">
              <tr>
                <th className="pb-2 font-medium">When</th>
                <th className="pb-2 font-medium">Who</th>
                <th className="pb-2 font-medium">Action</th>
                <th className="pb-2 font-medium">Result</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-zinc-200 dark:divide-zinc-800">
              {audit.data?.map((e) => (
                <tr key={e.id}>
                  <td className="py-1 whitespace-nowrap text-zinc-500" title={new Date(e.createdAt).toLocaleString()}>
                    {timeAgo(e.createdAt)}
                  </td>
                  <td className="py-1">{e.actor}</td>
                  <td className="py-1 font-mono">
                    {e.action}
                    {e.target && <span className="text-zinc-500"> {e.target}</span>}
                  </td>
                  <td className={`py-1 ${e.status >= 400 ? "text-red-600" : "text-zinc-500"}`} title={e.error}>
                    {e.status}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}
