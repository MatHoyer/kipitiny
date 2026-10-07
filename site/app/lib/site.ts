/** Where the site is published; canonical URLs, the sitemap and social cards use it. */
export const SITE_URL = (import.meta.env.VITE_SITE_URL || "https://kipitiny.mathieuhoyer.fr").replace(/\/$/, "");

/** The kipitiny version the site is built from ("" if unknown). */
export const VERSION: string = __KIPITINY_VERSION__;

export const GITHUB = "https://github.com/MatHoyer/kipitiny";

/** Title, description, canonical URL and social card tags for one page. */
export function seo({ title, description, path }: { title: string; description: string; path: string }) {
  const url = SITE_URL + path;
  const image = `${SITE_URL}/og.png`;
  return [
    { title },
    { name: "description", content: description },
    { tagName: "link", rel: "canonical", href: url },
    { property: "og:type", content: "website" },
    { property: "og:site_name", content: "kipitiny" },
    { property: "og:title", content: title },
    { property: "og:description", content: description },
    { property: "og:url", content: url },
    { property: "og:image", content: image },
    { property: "og:image:width", content: "1200" },
    { property: "og:image:height", content: "630" },
    { name: "twitter:card", content: "summary_large_image" },
    { name: "twitter:image", content: image },
  ];
}
