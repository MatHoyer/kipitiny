import { docs } from "~/lib/docs.server";
import { SITE_URL } from "~/lib/site";

export function loader() {
  const paths = ["/", "/docs", ...docs.map((d) => `/docs/${d.slug}`)];
  const urls = paths.map((p) => `  <url><loc>${SITE_URL}${p}</loc></url>`).join("\n");
  return new Response(
    `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${urls}\n</urlset>\n`,
    { headers: { "Content-Type": "application/xml; charset=utf-8" } },
  );
}
