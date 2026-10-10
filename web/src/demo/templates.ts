import type { AppTemplate, Service } from "../api";

/**
 * The manager's templates (internal/templates/files), as the demo shows
 * them: what the gallery lists and the services an install creates.
 */
/** domainInput names the input giving the service its domain. */
type DemoService = Partial<Service> & Pick<Service, "name" | "image"> & { domainInput?: string };

export const demoTemplates: (AppTemplate & { services: DemoService[] })[] = [
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
    services: [
      { name: "hub", image: "henrygd/beszel:0.21.0", icon: "beszel", port: 8090, domainInput: "DOMAIN", volumes: [{ name: "data", path: "/beszel_data" }] },
    ],
  },
  {
    id: "minecraft",
    title: "Minecraft server",
    description: "A Minecraft Java Edition server, vanilla or Paper, Fabric, Forge. The world is kept in a volume and backed up like any other.",
    website: "https://www.minecraft.net",
    docs: "https://docker-minecraft-server.readthedocs.io",
    icon: "minecraft",
    project: "minecraft",
    inputs: [
      {
        name: "EULA",
        label: "I accept the Minecraft EULA",
        type: "checkbox",
        required: true,
        help: "Mojang's terms for running a server, https://aka.ms/MinecraftEULA. The server doesn't start without it.",
      },
      { name: "SERVER_TYPE", label: "Server type", type: "select", options: ["VANILLA", "PAPER", "FABRIC", "FORGE", "NEOFORGE", "PURPUR"], default: "VANILLA" },
      { name: "VERSION", label: "Minecraft version", type: "text", default: "LATEST", help: "LATEST, or a version like 1.21.4." },
      { name: "MEMORY", label: "Memory", type: "text", default: "2g", help: "The container's memory limit; the server gets three quarters of it." },
      { name: "PORT", label: "Port", type: "text", default: "25565", help: "The host port players connect to. Open it in the server's firewall." },
      { name: "DIFFICULTY", label: "Difficulty", type: "select", options: ["peaceful", "easy", "normal", "hard"], default: "normal" },
      { name: "MODE", label: "Game mode", type: "select", options: ["survival", "creative", "adventure"], default: "survival" },
      { name: "MOTD", label: "Message of the day", type: "text", default: "A kipitiny Minecraft server" },
      {
        name: "DOMAIN",
        label: "Domain",
        type: "domain",
        placeholder: "mc.example.com",
        help: "A DNS name pointing at this server, for players to connect with (with the port when it isn't 25565).",
      },
      { name: "RCON_PASSWORD", label: "RCON password", type: "secret", generate: 24 },
    ],
    compose: "",
    services: [
      {
        name: "server",
        image: "itzg/minecraft-server:2026.9.2-java25",
        icon: "minecraft",
        domainInput: "DOMAIN",
        memoryMb: 2048,
        publishedPorts: [{ hostPort: 25565, containerPort: 25565, protocol: "tcp" }],
        volumes: [{ name: "data", path: "/data" }],
      },
    ],
  },
];
