import { readdirSync } from "node:fs";
import type { Config } from "@react-router/dev/config";

// Every page is prerendered to static HTML; nginx serves build/client.
const slugs = readdirSync("docs")
  .filter((f) => f.endsWith(".md"))
  .map((f) => f.slice(0, -3));

export default {
  ssr: false,
  routeDiscovery: { mode: "initial" },
  prerender: ["/", "/docs", "/llms.txt", "/sitemap.xml", "/robots.txt", ...slugs.flatMap((s) => [`/docs/${s}`, `/docs/${s}/llms.txt`])],
} satisfies Config;
