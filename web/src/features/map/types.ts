import type { Container, PublishedPort, ServiceKind } from "@/features/services/types";
import type { DNSStatus } from "@/features/settings/types";

/** A container with the networks it is attached to. */
export type TopoNode = Container & {
  endpoints: { network: string; ip?: string; aliases?: string[] }[];
};

export type TopoService = {
  id: string;
  name: string;
  kind: ServiceKind;
  image: string;
  icon?: string;
  domain?: string;
  port?: number;
  replicas: number;
  stopped?: boolean;
  sleeping?: boolean;
  preview?: "only" | "skip";
  /** Volume holding a database's data. */
  volume?: string;
  /** IDs of the project databases its env references. */
  uses: string[];
  /** IDs of the networks created by hand it joins. */
  networks: string[];
  hostNetwork?: boolean;
  /** Host ports published straight to the service. */
  publishedPorts?: PublishedPort[];
  routePending?: boolean;
  /** The networks its running containers miss, e.g. one removed outside kipitiny. */
  networkIssues?: string[];
  dns?: DNSStatus;
  containers: TopoNode[];
};

export type TopoNetwork = {
  name: string;
  subnet?: string;
  gateway?: string;
  /** Empty for the proxy network and the ones created by hand. */
  projectId?: string;
  /** Name and ID of a network created by hand. */
  custom?: string;
  customId?: string;
  /** Expected but not found on the server. */
  missing?: boolean;
};

/** gitPath: the compose file the project follows, when it's linked to git; parentId: the project a preview copies. */
export type TopoProject = { id: string; name: string; network: string; gitPath?: string; parentId?: string; services: TopoService[] };

export type ServerTopology = {
  id: string;
  name: string;
  kind: "local" | "ssh";
  /** Set when the server's Docker could not be read. */
  error?: string;
  /** False when routing is left to the user's own proxy. */
  traefik: boolean;
  tunnel: boolean;
  entrypoints: { name: string; port: number; hostPort?: string; redirectTo?: string }[];
  proxy?: TopoNode;
  cloudflared?: TopoNode;
  manager?: { domain?: string; upstream: string; container?: TopoNode };
  networks: TopoNetwork[];
  projects: TopoProject[];
};

export type Topology = { servers: ServerTopology[] };
