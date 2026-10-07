import { existsSync, readdirSync } from "node:fs";
import type { Config } from "@react-router/dev/config";

// Every page is prerendered to static HTML; nginx serves build/client.
const pages = (dir: string, base: string) => {
  const slugs = readdirSync(dir)
    .filter((f) => f.endsWith(".md"))
    .map((f) => f.slice(0, -3));
  return [base, ...slugs.flatMap((s) => [`${base}/${s}`, `${base}/${s}/llms.txt`])];
};
const versions = existsSync("versions") ? readdirSync("versions") : [];

export default {
  ssr: false,
  routeDiscovery: { mode: "initial" },
  prerender: [
    "/",
    "/llms.txt",
    "/sitemap.xml",
    "/robots.txt",
    ...pages("docs", "/docs"),
    ...versions.flatMap((v) => [`/docs/${v}/llms.txt`, ...pages(`versions/${v}`, `/docs/${v}`)]),
  ],
} satisfies Config;
