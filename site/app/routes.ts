import { type RouteConfig, index, layout, route } from "@react-router/dev/routes";

export default [
  index("routes/home.tsx"),
  route("llms.txt", "routes/llms.ts"),
  route("sitemap.xml", "routes/sitemap.ts"),
  route("robots.txt", "routes/robots.ts"),
  route("docs/:slug/llms.txt", "routes/doc-llms.ts"),
  layout("routes/docs-layout.tsx", [
    route("docs", "routes/docs-index.tsx"),
    route("docs/:slug", "routes/doc.tsx"),
  ]),
] satisfies RouteConfig;
