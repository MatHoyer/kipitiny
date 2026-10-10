import { EthernetPort, Globe, Lock, Server, type LucideIcon } from "lucide-react";
import type { PublishedPort } from "@/api";
import { cn } from "@/lib/utils";

export const portLabel = (p: PublishedPort) =>
  `${p.hostPort}${p.containerPort !== p.hostPort ? `→${p.containerPort}` : ""}/${p.protocol}`;

type Reachable = { domain?: string; port?: number; publishedPorts?: PublishedPort[]; hostNetwork?: boolean };

/**
 * How an app is reached from outside: HTTP on its domain, ports published
 * on the server (public, whatever the domain), the host's network, or
 * nothing (private to its project).
 */
export function reachOf(s: Reachable): { kind: "http" | "ports" | "host" | "private"; label: string } {
  const ports = s.publishedPorts ?? [];
  if (s.domain && s.port) return { kind: "http", label: s.domain };
  if (ports.length) return { kind: "ports", label: s.domain ? `${s.domain}:${ports[0].hostPort}` : ports.map(portLabel).join(", ") };
  if (s.hostNetwork) return { kind: "host", label: "host network" };
  return { kind: "private", label: "private" };
}

const icons: Record<ReturnType<typeof reachOf>["kind"], [LucideIcon, string?]> = {
  http: [Globe, "text-sky-500"],
  ports: [EthernetPort, "text-amber-500"],
  host: [Server, "text-amber-500"],
  private: [Lock],
};

export function ReachIcon({ service, className }: { service: Reachable; className?: string }) {
  const [Icon, color] = icons[reachOf(service).kind];
  return <Icon className={cn(color, className)} />;
}
