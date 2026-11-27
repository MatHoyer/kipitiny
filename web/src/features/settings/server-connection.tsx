import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, Plus } from "lucide-react";
import { useId, useState, type FormEvent, type ReactNode } from "react";
import { useNavigate } from "react-router";
import { toast } from "sonner";
import { Mono } from "@/components/common";
import { CheckboxField } from "@kipit-labs/kipitidy/checkbox-field";
import { Loading } from "@kipit-labs/kipitidy/loading";
import { Section } from "@kipit-labs/kipitidy/section";
import { HowTo } from "@/components/how-to";
import { StepsProgress, type PlannedStep } from "@/features/settings/test-dialog";
import { SaveBar } from "@/components/save-bar";
import { Button } from "@kipit-labs/kipitidy/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@kipit-labs/kipitidy/dialog";
import { FloatingInput } from "@kipit-labs/kipitidy/floating-input";
import { api, type Server, type ServerInput } from "@/api";

function useServerForm(server: Server | null, onSaved: (s: Server) => void) {
  const qc = useQueryClient();
  const initial = {
    name: server?.name ?? "",
    host: server?.host ?? "",
    port: String(server?.port ?? 22),
    sshUser: server?.sshUser ?? "root",
    socket: server?.socket ?? "/var/run/docker.sock",
  };
  const [form, setForm] = useState(initial);
  const [resetHostKey, setResetHostKey] = useState(false);
  // Steps the manager reported while reaching and preparing the server.
  const [seen, setSeen] = useState<string[]>([]);
  const save = useMutation({
    // A new server's dialog shows where it failed instead.
    meta: { error: server ? "Couldn't save the server" : false },
    mutationFn: () => {
      const input: ServerInput = { ...form, port: Number(form.port) || 22, resetHostKey };
      const onStep = (s: string) => setSeen((prev) => [...prev, s]);
      setSeen([]);
      return server ? api.updateServer(server.id, input, onStep) : api.createServer(input, onStep);
    },
    onSuccess: (s) => {
      qc.invalidateQueries({ queryKey: ["servers"] });
      setResetHostKey(false);
      onSaved(s);
    },
  });
  const dirty = resetHostKey || JSON.stringify(form) !== JSON.stringify(initial);
  const reset = () => {
    setForm(initial);
    setResetHostKey(false);
  };
  return { form, setForm, resetHostKey, setResetHostKey, save, seen, dirty, reset };
}

/** What adding or saving a server does, as the manager reports it. */
const serverPlan: PlannedStep[] = [
  { id: "connect", title: "Connect over SSH", hint: "Pins the server's host key the first time, then asks Docker for its version" },
  { id: "network", title: "Create the proxy network", hint: "Shared by the apps and the reverse proxy" },
  { id: "traefik", title: "Start Traefik", hint: "The reverse proxy serving the apps' domains" },
  { id: "tunnel", title: "Start the Cloudflare tunnel", hint: "Only when the server is reached through one" },
];

/** What the server needs before the manager can reach it, step by step. */
function SshSetup() {
  const key = useQuery({ queryKey: ["ssh-key"], queryFn: api.sshKey });
  return (
    <HowTo
      label="How do I prepare the server?"
      accent="#2496ED"
      steps={[
        {
          title: "Install Docker",
          text: "Skip it if Docker already runs there.",
          copy: "curl -fsSL https://get.docker.com | sh",
        },
        {
          title: "Authorize the manager's key",
          text: "As the SSH user. The server's host key is pinned on first connection.",
          extra: !key.data && <Loading className="py-2" />,
          copy: key.data && `mkdir -p ~/.ssh && echo '${key.data.publicKey}' >> ~/.ssh/authorized_keys`,
        },
        {
          title: "Let it use Docker",
          text: "Unless it's root: the SSH user needs the Docker socket. Log in again for it to apply.",
          copy: "sudo usermod -aG docker $USER",
        },
        {
          title: "Allow socket forwarding",
          text: (
            <>
              The Docker API is reached through its socket over SSH, never over TCP. <Mono>sshd</Mono> must allow it (the service is{" "}
              <Mono>sshd</Mono> on some distributions).
            </>
          ),
          copy: "printf 'AllowTcpForwarding local\\nAllowStreamLocalForwarding yes\\n' | sudo tee /etc/ssh/sshd_config.d/kipitiny.conf && sudo systemctl reload ssh",
        },
      ]}
    />
  );
}

function ServerFields({
  f,
  hostKey,
  autoFocus,
}: {
  f: ReturnType<typeof useServerForm>;
  hostKey?: boolean;
  autoFocus?: boolean;
}): ReactNode {
  const set = (k: keyof typeof f.form) => (e: { target: { value: string } }) => f.setForm({ ...f.form, [k]: e.target.value });
  return (
    <div className="grid gap-4 sm:grid-cols-2">
      <FloatingInput label="Name" required autoFocus={autoFocus} value={f.form.name} onChange={set("name")} placeholder="db-server-2" />
      <FloatingInput label="Host" required value={f.form.host} onChange={set("host")} placeholder="203.0.113.10" />
      <FloatingInput label="SSH port" type="number" min={1} max={65535} value={f.form.port} onChange={set("port")} />
      <FloatingInput label="SSH user" required value={f.form.sshUser} onChange={set("sshUser")} />
      <FloatingInput
        label="Docker socket"
        required
        value={f.form.socket}
        onChange={set("socket")}
        inputClassName="font-mono"
        className="sm:col-span-2"
      />
      {hostKey && (
        <CheckboxField
          label="Forget the pinned host key"
          description="Only after reinstalling the server."
          checked={f.resetHostKey}
          onCheckedChange={f.setResetHostKey}
          className="sm:col-span-2"
        />
      )}
    </div>
  );
}

export function ConnectionSection({ server }: { server: Server }) {
  const f = useServerForm(server, () => toast.success("Server saved"));
  const formId = useId();
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    f.save.mutate();
  };
  return (
    <Section title="SSH connection">
      <form id={formId} onSubmit={onSubmit} className="space-y-4">
        <ServerFields f={f} hostKey={!!server.hostKey} />
        <SshSetup />
      </form>
      <SaveBar form={formId} dirty={f.dirty} saving={f.save.isPending} onReset={f.reset} />
    </Section>
  );
}

export function NewServerDialog() {
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  // Stays open on its finished steps: the server is opened from there.
  const f = useServerForm(null, () => {});
  const added = f.save.isSuccess ? f.save.data : null;
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    f.save.mutate();
  };
  const onOpenChange = (next: boolean) => {
    setOpen(next);
    if (!next && added) {
      f.reset();
      f.save.reset();
    }
  };
  const started = f.save.isPending || !!f.save.error || !!added;
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button size="sm">
          <Plus data-icon="inline-start" />
          Add server
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={onSubmit} className="contents">
          <DialogHeader>
            <DialogTitle>New server</DialogTitle>
            <DialogDescription>A Docker host reached over SSH.</DialogDescription>
          </DialogHeader>
          {(!started || f.save.error) && <SshSetup />}
          <ServerFields f={f} autoFocus />
          {started && (
            <div className="grid gap-2.5 rounded-lg border bg-muted/30 p-3 animate-in fade-in slide-in-from-top-1">
              <p className="text-xs font-medium text-muted-foreground">{f.save.isPending ? "Connecting…" : added ? "Added." : "Couldn't add it."}</p>
              <StepsProgress plan={serverPlan} seen={f.seen} running={f.save.isPending} done={!!added} error={f.save.error} accent="#2496ED" />
            </div>
          )}
          <DialogFooter>
            {added ? (
              <Button
                type="button"
                onClick={() => {
                  onOpenChange(false);
                  navigate(`/servers/${added.id}`);
                }}
              >
                Open {added.name}
                <ArrowRight data-icon="inline-end" />
              </Button>
            ) : (
              <Button type="submit" loading={f.save.isPending}>
                Add server
              </Button>
            )}
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
