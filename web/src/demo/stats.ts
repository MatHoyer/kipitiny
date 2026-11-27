import type { Container, Project, HostUsage, SecurityReport, ServerStats, ServiceStats, Topology, TopoNode, TopoService, Usage } from "@/api";
import { type DemoDb } from "./seed";
import { containers, db, hash, isDb, projectNet, projectOf } from "./state";

export function topology(projectId?: string): Topology {
  const projects = projectId ? [projectOf(projectId)] : db.projects;
  let ip = 2;
  const node = (c: Container, endpoints: TopoNode["endpoints"]): TopoNode => ({ ...c, endpoints });
  const used = (s: DemoDb["services"][number], p: Project) =>
    db.services.filter((d) => d.projectId === p.id && isDb(d) && Object.values(s.env).some((v) => v.includes(`db.${d.name}.`))).map((d) => d.id);
  const proxy: TopoNode = node(
    { id: hash("traefik"), name: "kipitiny-traefik", replica: 0, deployId: "", image: "traefik:v3.7.17", state: "running", status: "Up 9 days" },
    [{ network: "kipitiny-proxy", ip: "172.18.0.2" }],
  );
  return {
    servers: [
      {
        id: "local",
        name: "This server",
        kind: "local",
        traefik: true,
        tunnel: false,
        entrypoints: [
          { name: "web", port: 80, hostPort: "80", redirectTo: "websecure" },
          { name: "websecure", port: 443, hostPort: "443" },
        ],
        proxy,
        manager: { domain: "kipitiny.acme.example", upstream: "kipitiny-manager:3000" },
        networks: [
          { name: "kipitiny-proxy", subnet: "172.18.0.0/16", gateway: "172.18.0.1" },
          ...db.networks.map((n, i) => ({ name: `kipitiny-net-${n.id}`, custom: n.name, customId: n.id, subnet: `172.${30 + i}.0.0/16` })),
          ...projects.map((p, i) => ({ name: projectNet(p), projectId: p.id, subnet: `172.${20 + i}.0.0/16`, gateway: `172.${20 + i}.0.1` })),
        ],
        projects: projects.map((p, pi) => ({
          id: p.id,
          name: p.name,
          network: projectNet(p),
          parentId: p.parentId,
          services: db.services
            .filter((s) => s.projectId === p.id)
            .map(
              (s): TopoService => ({
                id: s.id,
                name: s.name,
                kind: s.kind,
                image: s.image,
                icon: s.icon || undefined,
                domain: s.domain || undefined,
                port: s.port || undefined,
                replicas: s.replicas,
                stopped: s.stopped || undefined,
                sleeping: s.sleeping || undefined,
                volume: isDb(s) ? `kipitiny-${s.id}-data` : undefined,
                uses: used(s, p),
                networks: s.networks,
                hostNetwork: s.hostNetwork || undefined,
                publishedPorts: s.publishedPorts.length ? s.publishedPorts : undefined,
                routePending: s.routePending || undefined,
                containers: containers(s).map((c) =>
                  node(c, [
                    { network: projectNet(p), ip: `172.${20 + pi}.0.${ip++}`, aliases: [s.name] },
                    ...(s.domain ? [{ network: "kipitiny-proxy", ip: `172.18.0.${ip++}` }] : []),
                    ...s.networks.map((id) => ({ network: `kipitiny-net-${id}`, ip: `172.30.0.${ip++}`, aliases: [`${p.name}-${s.name}`] })),
                  ]),
                ),
              }),
            ),
        })),
      },
    ],
  };
}

/** A gentle, stable-looking usage curve per service. */
export function usage(s: DemoDb["services"][number], at: number): Usage {
  const n = isDb(s) ? 1 : s.replicas;
  const seed = parseInt(hash(s.id).slice(0, 4), 16);
  const wave = Math.sin(at / 60_000 + seed) * 0.5 + 0.5;
  const base = s.kind === "postgres" ? 3 : s.kind === "redis" ? 1 : s.image.startsWith("nginx") ? 0.4 : 6;
  const mem = (s.kind === "postgres" ? 140 : s.kind === "redis" ? 12 : s.image.startsWith("nginx") ? 9 : 85) * 2 ** 20;
  return {
    at: new Date(at).toISOString(),
    replicas: n,
    cpu: Math.round((base + wave * base) * n * 10) / 10,
    memoryBytes: Math.round(mem * n * (0.95 + wave * 0.1)),
    memoryLimitBytes: s.memoryMb ? s.memoryMb * n * 2 ** 20 : undefined,
    netRx: Math.round((s.domain ? 40_000 : 6_000) * (0.5 + wave)),
    netTx: Math.round((s.domain ? 90_000 : 9_000) * (0.5 + wave)),
  };
}

export function serviceStats(s: DemoDb["services"][number]): ServiceStats {
  const t = Date.now();
  const history = s.stopped ? [] : Array.from({ length: 30 }, (_, i) => usage(s, t - (29 - i) * 10_000));
  const per = Object.fromEntries(containers(s).map((c) => [c.id.slice(0, 12), { current: history.at(-1) ?? null, history }]));
  return { current: history.at(-1) ?? null, history, containers: per };
}

/** A typical fresh VPS: patched by hand so far, SSH still open to passwords. */
export function securityReport(): SecurityReport {
  return {
    checkedAt: new Date(Date.now() - 2 * 3600_000).toISOString(),
    os: "Ubuntu 24.04.1 LTS",
    items: [
      { id: "updates", title: "Security updates", status: "warn", detail: "3 security updates pending (12 updates in all).", hint: "Install them, or turn on automatic updates." },
      { id: "reboot", title: "Reboot", status: "warn", detail: "A reboot is needed to finish installing updates.", hint: "Reboot at a quiet time: every app on this server restarts." },
      { id: "auto_updates", title: "Automatic security updates", status: "fail", detail: "Off: security fixes wait for someone to install them.", hint: "Install and enable unattended-upgrades." },
      { id: "ssh_password", title: "SSH password login", status: "fail", detail: "On: passwords can be guessed from the internet.", hint: "Set PasswordAuthentication no, once your key logs in." },
      { id: "ssh_root", title: "SSH root login", status: "ok", detail: "root: without-password." },
      { id: "firewall", title: "Firewall", status: "fail", detail: "No active firewall (ufw, firewalld or nftables).", hint: "Allow SSH, then deny other incoming traffic. Ports published by Docker go around it either way." },
      { id: "fail2ban", title: "Brute-force protection", status: "warn", detail: "fail2ban isn't running: SSH login attempts aren't throttled.", hint: "Install fail2ban with its sshd jail." },
      { id: "ports", title: "Open ports", status: "ok", detail: "Only SSH (22) and ports published by Docker." },
    ],
    reboot: true,
    updates: [
      { name: "linux-image-6.8.0-50-generic", version: "6.8.0-50.51", security: true, impact: "reboot" },
      { name: "openssh-server", version: "1:9.6p1-3ubuntu13.8", security: true, impact: "ssh" },
      { name: "openssl", version: "3.0.13-0ubuntu3.5", security: true },
      { name: "containerd.io", version: "1.7.24-1", security: false, impact: "docker" },
      { name: "docker-ce", version: "5:27.4.0-1~ubuntu.24.04~noble", security: false, impact: "docker" },
      ...["curl", "git", "libcurl4t64", "python3.12", "tzdata", "vim", "wget"].map((name) => ({ name, version: "1.0-1ubuntu1", security: false })),
    ],
  };
}

/** A host that's busy enough to look alive: a slow wave over a fixed machine. */
export function hostStats(): ServerStats {
  const t = Date.now();
  const history = Array.from({ length: 30 }, (_, i): HostUsage => {
    const at = t - (29 - i) * 10_000;
    const wave = (Math.sin(at / 60_000) + 1) / 2;
    return {
      at: new Date(at).toISOString(),
      cpu: 8 + wave * 14,
      cores: 4,
      load: [0.4 + wave, 0.6, 0.55],
      memoryBytes: Math.round((3.1 + wave * 0.4) * 2 ** 30),
      memoryTotalBytes: 8 * 2 ** 30,
      swapBytes: 0,
      swapTotalBytes: 2 * 2 ** 30,
      diskBytes: 31 * 2 ** 30,
      diskTotalBytes: 80 * 2 ** 30,
      uptimeSec: 12 * 86400 + at / 1000 - t / 1000,
    };
  });
  return { current: history.at(-1) ?? null, history };
}
