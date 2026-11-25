import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Trash2 } from "lucide-react";
import { useId, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { toast } from "sonner";
import { CheckboxField, CopyField, Empty, ErrorText, Loading, Mono, Section } from "@/components/common";
import { DomainField } from "@/components/domain-field";
import { SaveBar } from "@/components/save-bar";
import { Button } from "@/components/ui/button";
import { FloatingInput } from "@/components/ui/floating-input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { api, type PublishedPort, type Route, type Service as ServiceT } from "../../api";
import { formatDateTime } from "@/lib/format";
import { Applies } from "./parts";

/** A domain without an HTTP port only names the server: Traefik doesn't route it. */
export const dnsOnlyHint = (ports: PublishedPort[]) =>
  `DNS only: connections go straight to ${ports.length > 1 ? "ports" : "port"} ${ports.map((p) => p.hostPort).join(", ")} on the server, not through Traefik.`;

type RouteRow = { paths: string; scheme: "http" | "h2c"; port: string };

const domainForm = (s: ServiceT) => ({
  domain: s.domain,
  port: s.port ? String(s.port) : "",
  routes: (s.routes ?? []).map((r): RouteRow => ({ paths: r.paths.join(" "), scheme: r.scheme ?? "http", port: r.port ? String(r.port) : "" })),
});

const toRoutes = (rows: RouteRow[]): Route[] =>
  rows.map((r) => ({
    paths: r.paths.split(/[\s,]+/).filter(Boolean),
    ...(r.scheme === "h2c" ? { scheme: "h2c" as const } : {}),
    ...(Number(r.port) ? { port: Number(r.port) } : {}),
  }));

/** The public domain, the HTTP port Traefik routes it to, and the paths routed to the app when it shares the domain. */
export function DomainCard({ svc }: { svc: ServiceT }) {
  const qc = useQueryClient();
  const [form, setForm] = useState(() => domainForm(svc));
  const dirty = JSON.stringify(form) !== JSON.stringify(domainForm(svc));
  const formId = useId();
  const save = useMutation({
    meta: { error: "Couldn't save the domain" },
    mutationFn: () =>
      api.updateService(svc.id, { domain: form.domain.trim(), port: Number(form.port) || 0, routes: toRoutes(form.routes) }),
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
  const setRoute = (i: number, field: keyof RouteRow) => (value: string) =>
    setForm({ ...form, routes: form.routes.map((r, j) => (j === i ? { ...r, [field]: value } : r)) });
  const routed = form.domain.trim() !== "" && Number(form.port) > 0;

  return (
    <Section title={<Applies title="Domain" when="deploy" />}>
      <form id={formId} onSubmit={onSubmit} className="space-y-4">
        <div className="grid gap-4 sm:grid-cols-3">
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
        </div>
        {(routed || form.routes.length > 0) && (
          <div className="space-y-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div>
                <Label>Path routes</Label>
                <p className="text-sm text-muted-foreground">
                  {form.routes.length === 0
                    ? "None: the whole domain goes to this app. With routes, only their paths do, and another service of the project can take the rest of the domain."
                    : "Only these path prefixes reach this app (the longest wins). Use h2c for gRPC."}
                </p>
              </div>
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={() => setForm({ ...form, routes: [...form.routes, { paths: "", scheme: "http", port: "" }] })}
              >
                <Plus data-icon="inline-start" />
                Add route
              </Button>
            </div>
            {form.routes.map((r, i) => (
              <div key={i} className="flex flex-wrap items-start gap-2 sm:flex-nowrap">
                <FloatingInput
                  label="Paths"
                  required
                  value={r.paths}
                  onChange={(e) => setRoute(i, "paths")(e.target.value)}
                  placeholder="/api /ws"
                  title="Path prefixes, separated by spaces or commas."
                  className="w-full sm:min-w-0 sm:flex-1"
                  inputClassName="font-mono"
                />
                <Select value={r.scheme} onValueChange={setRoute(i, "scheme")}>
                  <SelectTrigger aria-label="Scheme" className="w-32 shrink-0 rounded-xl data-[size=default]:h-14">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="http">HTTP</SelectItem>
                    <SelectItem value="h2c">h2c (gRPC)</SelectItem>
                  </SelectContent>
                </Select>
                <FloatingInput
                  label="Port"
                  type="number"
                  min={1}
                  max={65535}
                  value={r.port}
                  onChange={(e) => setRoute(i, "port")(e.target.value)}
                  placeholder={form.port || "app's"}
                  className="w-28 shrink-0"
                  inputClassName="font-mono"
                />
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  title="Remove"
                  aria-label={`Remove route ${r.paths}`}
                  className="mt-3.5 text-muted-foreground hover:text-destructive"
                  onClick={() => setForm({ ...form, routes: form.routes.filter((_, j) => j !== i) })}
                >
                  <Trash2 />
                </Button>
              </div>
            ))}
          </div>
        )}
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

/** Whether the analytics server counts the domain's visits. */
export function AnalyticsCard({ svc }: { svc: ServiceT }) {
  const qc = useQueryClient();
  const id = useId();
  const save = useMutation({
    meta: { error: "Couldn't change analytics" },
    mutationFn: (analytics: boolean) => api.updateService(svc.id, { analytics }),
    onSuccess: (updated) => qc.setQueryData(["service", svc.id], updated),
  });
  return (
    <Section
      title={<Applies title="Analytics" when="now" />}
      description={
        <>
          Counts visits of <Mono>{svc.domain}</Mono> without cookies, once analytics are on in Settings › Analytics. The pages need the
          script tag shown in the Analytics tab.
        </>
      }
    >
      <div className="flex items-center gap-3">
        <Switch id={id} checked={svc.analytics} disabled={save.isPending} onCheckedChange={(v) => save.mutate(v)} />
        <Label htmlFor={id}>Count visits</Label>
      </div>
      {svc.analytics && <ShareLink svc={svc} />}
    </Section>
  );
}

/** The app's public, read-only Analytics tab: one link at a time, its token shown once. */
function ShareLink({ svc }: { svc: ServiceT }) {
  const qc = useQueryClient();
  const [token, setToken] = useState("");
  const refresh = () => qc.invalidateQueries({ queryKey: ["service", svc.id] });
  const share = useMutation({
    meta: { error: "Couldn't create the link" },
    mutationFn: () => api.shareAnalytics(svc.id),
    onSuccess: (res) => (setToken(res.token), refresh()),
  });
  const revoke = useMutation({
    meta: { error: "Couldn't revoke the link" },
    mutationFn: () => api.unshareAnalytics(svc.id),
    onSuccess: () => (setToken(""), refresh(), toast.success("Link revoked")),
  });
  const busy = share.isPending || revoke.isPending;
  return (
    <div className="space-y-3 border-t pt-4">
      <div>
        <p className="text-sm font-medium">Public link</p>
        <p className="text-sm text-muted-foreground">
          Anyone with it sees the Analytics tab without signing in: the numbers, lists and range picker, never the visits, identified users,
          event properties or settings.
        </p>
      </div>
      {token && (
        <div className="space-y-1.5">
          <CopyField value={`${location.origin}${import.meta.env.BASE_URL.replace(/\/$/, "")}/share/analytics/${token}`} />
          <p className="text-xs text-muted-foreground">Copy it now: it isn't shown again.</p>
        </div>
      )}
      <div className="flex flex-wrap items-center gap-2">
        {svc.analyticsSharedAt && !token && (
          <span className="text-sm text-muted-foreground">Shared since {formatDateTime(svc.analyticsSharedAt)}.</span>
        )}
        <Button variant="outline" size="sm" disabled={busy} onClick={() => share.mutate()}>
          {svc.analyticsSharedAt ? "New link" : "Create link"}
        </Button>
        {svc.analyticsSharedAt && (
          <Button variant="outline" size="sm" disabled={busy} onClick={() => revoke.mutate()}>
            Revoke
          </Button>
        )}
      </div>
      {svc.analyticsSharedAt && <p className="text-xs text-muted-foreground">A new link revokes the current one.</p>}
    </div>
  );
}
