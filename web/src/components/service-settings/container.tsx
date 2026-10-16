import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useId, useState, type FormEvent } from "react";
import { toast } from "sonner";
import { Mono, Section } from "@/components/common";
import { SaveBar } from "@/components/save-bar";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingTextarea } from "@/components/ui/floating-textarea";
import { api, type Runtime, type Service as ServiceT } from "../../api";
import { Applies } from "./parts";

/** Splits a command like a shell does with quotes and backslashes, as the manager does for compose files. */
export function splitCommand(s: string): string[] | null {
  const args: string[] = [];
  let cur = "";
  let inArg = false;
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (c === " " || c === "\t" || c === "\n") {
      if (inArg) args.push(cur);
      cur = "";
      inArg = false;
    } else if (c === "'") {
      const end = s.indexOf("'", i + 1);
      if (end < 0) return null;
      cur += s.slice(i + 1, end);
      i = end;
      inArg = true;
    } else if (c === '"') {
      for (i++; i < s.length && s[i] !== '"'; i++) {
        if (s[i] === "\\" && i + 1 < s.length && '"\\$`'.includes(s[i + 1])) i++;
        cur += s[i];
      }
      if (i >= s.length) return null;
      inArg = true;
    } else if (c === "\\" && i + 1 < s.length) {
      cur += s[++i];
      inArg = true;
    } else {
      cur += c;
      inArg = true;
    }
  }
  if (inArg) args.push(cur);
  return args;
}

/** The inverse of splitCommand: arguments with spaces or quotes are single-quoted. */
export function joinCommand(args: string[] | undefined) {
  return (args ?? []).map((a) => (/^[\w@%+=:,./-]+$/.test(a) ? a : `'${a.replaceAll("'", `'\\''`)}'`)).join(" ");
}

const labelsText = (labels: Record<string, string> | undefined) =>
  Object.entries(labels ?? {})
    .map(([k, v]) => `${k}=${v}`)
    .join("\n");

function parseLabels(text: string) {
  const out: Record<string, string> = {};
  for (const line of text.split("\n")) {
    if (!line.trim()) continue;
    const i = line.indexOf("=");
    out[(i < 0 ? line : line.slice(0, i)).trim()] = i < 0 ? "" : line.slice(i + 1);
  }
  return out;
}

const runtimeForm = (r: Runtime) => ({
  command: joinCommand(r.command),
  entrypoint: joinCommand(r.entrypoint),
  user: r.user ?? "",
  workingDir: r.workingDir ?? "",
  labels: labelsText(r.labels),
});

/** The settings only a compose file (or the API) sets, listed so the card shows everything that applies. */
function otherSettings(r: Runtime, files: ServiceT["files"]) {
  const out: [string, string][] = [];
  for (const f of files ?? []) out.push([f.secret ? "secret" : "file", `${f.path} (${f.size} B${f.source ? `, from ${f.source}` : ""})`]);
  for (const b of r.binds ?? []) out.push(["bind", b]);
  for (const [path, opts] of Object.entries(r.tmpfs ?? {})) out.push(["tmpfs", opts ? `${path}:${opts}` : path]);
  if (r.shmSizeMb) out.push(["shm_size", `${r.shmSizeMb} MB`]);
  for (const [name, u] of Object.entries(r.ulimits ?? {})) out.push(["ulimits", `${name} ${u.soft === u.hard ? u.soft : `${u.soft}:${u.hard}`}`]);
  for (const [k, v] of Object.entries(r.sysctls ?? {})) out.push(["sysctls", `${k}=${v}`]);
  for (const h of r.extraHosts ?? []) out.push(["extra_hosts", h]);
  if (r.stopSignal) out.push(["stop_signal", r.stopSignal]);
  if (r.readOnly) out.push(["read_only", "true"]);
  if (r.init) out.push(["init", "true"]);
  if (r.tty) out.push(["tty", "true"]);
  if (r.stdinOpen) out.push(["stdin_open", "true"]);
  for (const c of r.capAdd ?? []) out.push(["cap_add", c]);
  for (const c of r.capDrop ?? []) out.push(["cap_drop", c]);
  for (const d of r.devices ?? []) out.push(["devices", d]);
  if (r.privileged) out.push(["privileged", "true"]);
  for (const o of r.securityOpt ?? []) out.push(["security_opt", o]);
  return out;
}

/** Command, entrypoint, user, working directory and labels; the rarer compose settings are shown read-only. */
export function ContainerCard({ svc }: { svc: ServiceT }) {
  const qc = useQueryClient();
  const runtime = svc.runtime ?? {};
  const [form, setForm] = useState(() => runtimeForm(runtime));
  const dirty = JSON.stringify(form) !== JSON.stringify(runtimeForm(runtime));
  const formId = useId();
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });
  const command = splitCommand(form.command);
  const entrypoint = splitCommand(form.entrypoint);
  const others = otherSettings(runtime, svc.files);

  const save = useMutation({
    meta: { error: "Couldn't save the container settings" },
    mutationFn: () =>
      api.updateService(svc.id, {
        runtime: {
          ...runtime,
          command: command ?? [],
          entrypoint: entrypoint ?? [],
          user: form.user.trim(),
          workingDir: form.workingDir.trim(),
          labels: parseLabels(form.labels),
        },
      }),
    onSuccess: (updated) => {
      qc.setQueryData(["service", svc.id], updated);
      setForm(runtimeForm(updated.runtime ?? {}));
      toast.success("Container settings saved", { description: "Deploy to apply." });
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <Section
      title={<Applies title="Container" when="deploy" />}
      description="How the image runs. Empty fields keep what the image sets."
    >
      <form id={formId} onSubmit={onSubmit} className="grid gap-4 sm:grid-cols-2">
        <FloatingInput
          label="Command"
          value={form.command}
          onChange={set("command")}
          placeholder="worker --queue mail"
          inputClassName="font-mono"
          className="col-span-full"
          aria-invalid={!command}
          description={command ? "Replaces the image's CMD. Split like a shell does, without variables: use sh -c '…' for those." : "Unterminated quote."}
        />
        <FloatingInput
          label="Entrypoint"
          value={form.entrypoint}
          onChange={set("entrypoint")}
          inputClassName="font-mono"
          className="col-span-full"
          aria-invalid={!entrypoint}
          description={entrypoint ? <>Replaces the image's ENTRYPOINT; <Mono>''</Mono> clears it.</> : "Unterminated quote."}
        />
        <FloatingInput label="User" value={form.user} onChange={set("user")} placeholder="1000:1000" description="A name or UID, optionally with :group." />
        <FloatingInput label="Working directory" value={form.workingDir} onChange={set("workingDir")} placeholder="/app" />
        <FloatingTextarea
          label="Labels"
          value={form.labels}
          onChange={set("labels")}
          placeholder="com.example.team=shop"
          className="col-span-full"
          description="One key=value per line. traefik.* and kipitiny.* are the manager's."
        />
        {others.length > 0 && (
          <div className="col-span-full space-y-1.5 text-sm">
            <p className="text-muted-foreground">Also set, from the compose file:</p>
            <ul className="flex flex-wrap gap-1.5">
              {others.map(([k, v], i) => (
                <li key={i} className="rounded-md bg-muted px-2 py-0.5 font-mono text-xs">
                  {k}: {v}
                </li>
              ))}
            </ul>
          </div>
        )}
      </form>
      <SaveBar
        form={formId}
        dirty={dirty && !!command && !!entrypoint}
        saving={save.isPending}
        onReset={() => setForm(runtimeForm(runtime))}
        label="Save container settings"
      />
    </Section>
  );
}
