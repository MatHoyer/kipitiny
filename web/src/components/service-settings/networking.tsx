import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Trash2 } from "lucide-react";
import { useId, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { toast } from "sonner";
import { CheckboxField, Empty, ErrorText, Loading, Mono, Section } from "@/components/common";
import { DomainField } from "@/components/domain-field";
import { SaveBar } from "@/components/save-bar";
import { Button } from "@/components/ui/button";
import { FloatingInput } from "@/components/ui/floating-input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { api, type PublishedPort, type Service as ServiceT } from "../../api";
import { Applies } from "./parts";

/** A domain without an HTTP port only names the server: Traefik doesn't route it. */
export const dnsOnlyHint = (ports: PublishedPort[]) =>
  `DNS only: connections go straight to ${ports.length > 1 ? "ports" : "port"} ${ports.map((p) => p.hostPort).join(", ")} on the server, not through Traefik.`;

const domainForm = (s: ServiceT) => ({ domain: s.domain, port: s.port ? String(s.port) : "" });

/** The public domain and the HTTP port Traefik routes it to. */
export function DomainCard({ svc }: { svc: ServiceT }) {
  const qc = useQueryClient();
  const [form, setForm] = useState(() => domainForm(svc));
  const dirty = JSON.stringify(form) !== JSON.stringify(domainForm(svc));
  const formId = useId();
  const save = useMutation({
    meta: { error: "Couldn't save the domain" },
    mutationFn: () => api.updateService(svc.id, { domain: form.domain.trim(), port: Number(form.port) || 0 }),
    onSuccess: (updated) => {
      qc.setQueryData(["service", svc.id], updated);
      setForm(domainForm(updated));
      if (!updated.routePending) {
        toast.success("Domain saved");
        return;
      }
      toast.success("Domain saved", {
        description: "Traefik serves it after the next deploy.",
        duration: 15_000,
        action: { label: "Deploy now", onClick: () => deploy.mutate() },
      });
    },
  });
  const deploy = useMutation({
    meta: { error: "Couldn't start the deployment" },
    mutationFn: () => api.deploy(svc.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["deployments", svc.id] });
      qc.invalidateQueries({ queryKey: ["service", svc.id] });
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <Section title={<Applies title="Domain" when="deploy" />}>
      <form id={formId} onSubmit={onSubmit} className="grid gap-4 sm:grid-cols-3">
        <DomainField
          value={form.domain}
          onChange={(domain) => setForm({ ...form, domain })}
          note={!Number(form.port) && svc.publishedPorts.length > 0 ? dnsOnlyHint(svc.publishedPorts) : undefined}
          className="sm:col-span-2"
        />
        <FloatingInput
          label="Port"
          type="number"
          min={0}
          max={65535}
          value={form.port}
          onChange={(e) => setForm({ ...form, port: e.target.value })}
          description={
            svc.publishedPorts.length > 0 ? "HTTP port Traefik routes the domain to. Empty: the domain is only for DNS." : undefined
          }
        />
      </form>
      <SaveBar form={formId} dirty={dirty} saving={save.isPending} onReset={() => setForm(domainForm(svc))} label="Save domain" />
    </Section>
  );
}

type PortRow = { hostIp: string; hostPort: string; containerPort: string; protocol: PublishedPort["protocol"] };

const portRows = (ports: PublishedPort[]): PortRow[] =>
  ports.map((p) => ({ hostIp: p.hostIp ?? "", hostPort: String(p.hostPort), containerPort: String(p.containerPort), protocol: p.protocol }));

/** Host ports bound straight to an app's container, for traffic that isn't HTTP. */
export function PortsCard({ svc }: { svc: ServiceT }) {
  const qc = useQueryClient();
  const [rows, setRows] = useState<PortRow[]>(() => portRows(svc.publishedPorts));
  const dirty = JSON.stringify(rows) !== JSON.stringify(portRows(svc.publishedPorts));
  const formId = useId();
  const save = useMutation({
    meta: { error: "Couldn't save the ports" },
    mutationFn: () =>
      api.updateService(svc.id, {
        publishedPorts: rows.map((r) => ({
          hostIp: r.hostIp.trim(),
          hostPort: Number(r.hostPort) || 0,
          containerPort: Number(r.containerPort) || 0,
          protocol: r.protocol,
        })),
        // An app publishing ports runs one replica.
        ...(rows.length > 0 && svc.replicas !== 1 ? { replicas: 1 } : {}),
      }),
    onSuccess: (updated) => {
      qc.setQueryData(["service", svc.id], updated);
      setRows(portRows(updated.publishedPorts));
      toast.success("Ports saved", { description: "Deploy to apply." });
    },
  });
  const set = (i: number, field: keyof PortRow) => (value: string) =>
    setRows(rows.map((r, j) => (j === i ? { ...r, [field]: value } : r)));
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <Section
      title={<Applies title="Published ports" when="deploy" />}
      description="Ports of the server bound straight to the container, for traffic that isn't HTTP (a game server, MQTT…). The app then runs one replica, and deploys stop the old container before starting the new one: a short downtime. Open the ports in the server's firewall; a Cloudflare tunnel doesn't carry them."
      actions={
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={() => setRows([...rows, { hostIp: "", hostPort: "", containerPort: "", protocol: "tcp" }])}
        >
          <Plus data-icon="inline-start" />
          Add port
        </Button>
      }
    >
      {/* Always rendered: the save bar submits it, even to remove the last port. */}
      <form id={formId} onSubmit={onSubmit} className="space-y-3">
        {rows.length === 0 && <Empty>No published ports: the app is only reachable through its domain, if any.</Empty>}
        {rows.map((r, i) => (
          <div key={i} className="flex flex-wrap items-start gap-2 sm:flex-nowrap">
            <FloatingInput
              label="Address"
              value={r.hostIp}
              onChange={(e) => set(i, "hostIp")(e.target.value)}
              placeholder="all"
              title="The server address to bind, e.g. 127.0.0.1 for the server itself only. Empty: every address."
              className="w-full sm:w-36 sm:shrink-0"
              inputClassName="font-mono"
            />
            <FloatingInput
              label="Host port"
              required
              type="number"
              min={1}
              max={65535}
              value={r.hostPort}
              onChange={(e) => set(i, "hostPort")(e.target.value)}
              placeholder="25565"
              className="min-w-0 flex-1"
              inputClassName="font-mono"
            />
            <FloatingInput
              label="Container port"
              type="number"
              min={1}
              max={65535}
              value={r.containerPort}
              onChange={(e) => set(i, "containerPort")(e.target.value)}
              placeholder={r.hostPort || "same"}
              className="min-w-0 flex-1"
              inputClassName="font-mono"
            />
            <Select value={r.protocol} onValueChange={set(i, "protocol")}>
              <SelectTrigger aria-label="Protocol" className="w-24 shrink-0 rounded-xl data-[size=default]:h-14">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="tcp">TCP</SelectItem>
                <SelectItem value="udp">UDP</SelectItem>
              </SelectContent>
            </Select>
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              title="Remove"
              aria-label={`Remove port ${r.hostPort}`}
              className="mt-3.5 text-muted-foreground hover:text-destructive"
              onClick={() => setRows(rows.filter((_, j) => j !== i))}
            >
              <Trash2 />
            </Button>
          </div>
        ))}
      </form>
      <SaveBar form={formId} dirty={dirty} saving={save.isPending} onReset={() => setRows(portRows(svc.publishedPorts))} label="Save ports" />
    </Section>
  );
}

/** Networks created by hand the service joins, to reach services of other projects. */
export function NetworksCard({ svc, projectName }: { svc: ServiceT; projectName: string }) {
  const qc = useQueryClient();
  const networks = useQuery({ queryKey: ["networks"], queryFn: api.networks });
  const [selected, setSelected] = useState(svc.networks);
  const dirty = [...selected].sort().join() !== [...svc.networks].sort().join();
  const formId = useId();
  const available = networks.data?.filter((n) => n.serverId === svc.serverId) ?? [];
  const save = useMutation({
    meta: { error: "Couldn't save the networks" },
    mutationFn: () => api.setServiceNetworks(svc.id, selected),
    onSuccess: (updated) => {
      qc.setQueryData(["service", svc.id], updated);
      setSelected(updated.networks);
      toast.success("Networks saved", { description: "Applied to the running containers; nothing restarted." });
    },
    onSettled: () => qc.invalidateQueries({ queryKey: ["networks"] }),
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };
  const toggle = (id: string, on: boolean) => setSelected(on ? [...selected, id] : selected.filter((x) => x !== id));

  return (
    <Section
      title={<Applies title="Networks" when="now" />}
      description={
        <>
          Its project network is automatic: the project's services reach it as <Mono>{svc.name}</Mono>. On the networks below, services of
          other projects reach it as <Mono>{`${projectName}-${svc.name}`}</Mono>.
        </>
      }
    >
      <ErrorText error={networks.error} />
      <form id={formId} onSubmit={onSubmit} className="space-y-4">
        {networks.isPending ? (
          <Loading />
        ) : available.length === 0 ? (
          <Empty>
            No networks created by hand on this server yet: create one in{" "}
            <Link to="/networks" className="underline">
              Infrastructure › Networks
            </Link>
            .
          </Empty>
        ) : (
          available.map((n) => {
            const others = n.services.filter((m) => m.id !== svc.id);
            return (
              <CheckboxField
                key={n.id}
                label={n.name}
                description={others.length ? `With ${others.map((m) => m.alias).join(", ")}` : "No other services on it yet."}
                checked={selected.includes(n.id)}
                onCheckedChange={(v) => toggle(n.id, v)}
              />
            );
          })
        )}
      </form>
      <SaveBar form={formId} dirty={dirty} saving={save.isPending} onReset={() => setSelected(svc.networks)} label="Save networks" />
    </Section>
  );
}
