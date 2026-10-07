import { existsSync, readdirSync } from "node:fs";
import { type RouteConfig, index, layout, route } from "@react-router/dev/routes";

/** The docs of each minor release (site/versions/<minor>, see scripts/versions.sh) are served at /docs/<minor>. */
const versions = existsSync("versions") ? readdirSync("versions") : [];

const docs = (v?: string) => {
  const base = v ? `docs/${v}` : "docs";
  const id = (name: string) => (v ? { id: `${name}-${v}` } : {});
  return [
    route(`${base}/:slug/llms.txt`, "routes/doc-llms.ts", id("doc-llms")),
    layout("routes/docs-layout.tsx", id("docs-layout"), [
      route(base, "routes/docs-index.tsx", id("docs-index")),
      route(`${base}/:slug`, "routes/doc.tsx", id("doc")),
    ]),
  ];
};

export default [
  index("routes/home.tsx"),
  route("llms.txt", "routes/llms.ts"),
  route("sitemap.xml", "routes/sitemap.ts"),
  route("robots.txt", "routes/robots.ts"),
  ...docs(),
  ...versions.flatMap((v) => [route(`docs/${v}/llms.txt`, "routes/llms.ts", { id: `llms-${v}` }), ...docs(v)]),
] satisfies RouteConfig;
