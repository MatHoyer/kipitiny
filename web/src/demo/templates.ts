import type { AppTemplate, Service } from "../api";

/**
 * The manager's templates (internal/templates/files), as the demo shows
 * them: what the gallery lists and the services an install creates.
 */
export const demoTemplates: (AppTemplate & { services: (Partial<Service> & Pick<Service, "name" | "image">)[] })[] = [
  {
    id: "beszel-agent",
    title: "Beszel agent",
    description: "Reports this server's CPU, memory, disks, network and containers to a Beszel hub.",
    website: "https://beszel.dev",
    docs: "https://beszel.dev/guide/agent-installation",
    icon: "beszel",
    project: "beszel-agent",
    inputs: [
      {
        name: "KEY",
        label: "Hub public key",
        type: "text",
        required: true,
        placeholder: "ssh-ed25519 AAAA…",
        help: "In the hub, Add System shows it. Give the system this server's address and port 45876.",
      },
      { name: "TOKEN", label: "Token", type: "secret", help: "With the hub URL, the agent connects to the hub itself (no open port needed)." },
      { name: "HUB_URL", label: "Hub URL", type: "url", placeholder: "https://beszel.example.com", help: "Needed with the token." },
    ],
    compose: "",
    services: [{ name: "agent", image: "henrygd/beszel-agent:0.21.0", icon: "beszel", hostNetwork: true, dockerSocket: "ro" }],
  },
  {
    id: "beszel-hub",
    title: "Beszel hub",
    description: "Lightweight server monitoring with history, Docker stats and alerts. Install the Beszel agent on each server to watch.",
    website: "https://beszel.dev",
    docs: "https://beszel.dev/guide/getting-started",
    icon: "beszel",
    project: "beszel",
    inputs: [
      {
        name: "DOMAIN",
        label: "Domain",
        type: "domain",
        required: true,
        placeholder: "beszel.example.com",
        help: "Where the dashboard is served. The first visit creates the admin account.",
      },
    ],
    compose: "",
    services: [{ name: "hub", image: "henrygd/beszel:0.21.0", icon: "beszel", port: 8090, volumes: [{ name: "data", path: "/beszel_data" }] }],
  },
];
