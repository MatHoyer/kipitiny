import { Marked } from "marked";

/** One page of the documentation, from site/docs/<slug>.md. */
export type Doc = {
  slug: string;
  title: string;
  description: string;
  order: number;
  markdown: string;
};

export type DocLink = Pick<Doc, "slug" | "title" | "description">;

/** The docs at /docs, from site/docs: what main (or the release tag CI builds) says. */
const latestFiles = import.meta.glob<string>("../../docs/*.md", {
  query: "?raw",
  import: "default",
  eager: true,
});

/** One copy per minor release at /docs/<minor>, from site/versions/<minor> (scripts/versions.sh). */
const versionFiles = import.meta.glob<string>("../../versions/*/*.md", {
  query: "?raw",
  import: "default",
  eager: true,
});

function parse(path: string, raw: string): Doc {
  const slug = path.split("/").pop()!.replace(/\.md$/, "");
  const m = raw.match(/^---\n([\s\S]*?)\n---\n+/);
  if (!m) throw new Error(`${path}: missing front matter`);
  const meta: Record<string, string> = {};
  for (const line of m[1].split("\n")) {
    const i = line.indexOf(":");
    if (i > 0) meta[line.slice(0, i).trim()] = line.slice(i + 1).trim();
  }
  return {
    slug,
    title: meta.title ?? slug,
    description: meta.description ?? "",
    order: Number(meta.order ?? 99),
    markdown: raw.slice(m[0].length),
  };
}

/** The docs of one version ("" for the latest), and the path they're published under. */
export type DocSet = { version: string; base: string; docs: Doc[] };

function docSetOf(version: string, files: [string, string][]): DocSet {
  return {
    version,
    base: version ? `/docs/${version}` : "/docs",
    docs: files.map(([path, raw]) => parse(path, raw)).sort((a, b) => a.order - b.order),
  };
}

const byMinor = (a: string, b: string) => {
  const [am, an] = a.split(".").map(Number);
  const [bm, bn] = b.split(".").map(Number);
  return bm - am || bn - an;
};

export const latest = docSetOf("", Object.entries(latestFiles));

/** Published minor versions, newest first. */
export const versions: DocSet[] = [...new Set(Object.keys(versionFiles).map((p) => p.split("/").at(-2)!))]
  .sort(byMinor)
  .map((v) => docSetOf(v, Object.entries(versionFiles).filter(([p]) => p.split("/").at(-2) === v)));

/** The doc set a request is for: /docs/<minor>/... or the latest. */
export function docSetFor(request: Request): DocSet {
  // Client navigations load /docs/<minor>.data, prerendered alongside the HTML.
  const path = new URL(request.url).pathname.replace(/\.data$/, "");
  const v = path.match(/^\/docs\/(\d+\.\d+)(?:\/|$)/)?.[1];
  return versions.find((s) => s.version === v) ?? latest;
}

export const docLinks = (set: DocSet): DocLink[] => set.docs.map(({ slug, title, description }) => ({ slug, title, description }));

export const findDoc = (set: DocSet, slug: string | undefined) => set.docs.find((d) => d.slug === slug);

/** GitHub-style heading ids, so the docs' #links keep working. */
export const headingId = (text: string) =>
  text
    .toLowerCase()
    .replace(/<[^>]+>/g, "")
    .replace(/&[a-z0-9#]+;/g, "")
    .replace(/[^a-z0-9 _-]/g, "")
    .trim()
    .replace(/ /g, "-");

export type Heading = { id: string; text: string };

/** Renders a doc to HTML and lists its second-level headings. */
export function render(doc: Doc): { html: string; headings: Heading[] } {
  const headings: Heading[] = [];
  const md = new Marked({
    renderer: {
      heading({ tokens, depth }) {
        const html = this.parser.parseInline(tokens);
        const id = headingId(html);
        if (depth === 2) headings.push({ id, text: html.replace(/<[^>]+>/g, "") });
        return `<h${depth} id="${id}"><a class="anchor" href="#${id}" aria-hidden="true" tabindex="-1">#</a>${html}</h${depth}>\n`;
      },
      // Each cell carries its column's name, so a phone can show rows as
      // stacked blocks ("Default: :3000") instead of squeezed columns.
      table({ header, rows }) {
        const align = (a: string | null) => (a ? ` style="text-align:${a}"` : "");
        const labels = header.map((h) => this.parser.parseInline(h.tokens).replace(/<[^>]+>/g, "").replace(/"/g, "&quot;"));
        const head = header.map((h) => `<th${align(h.align)}>${this.parser.parseInline(h.tokens)}</th>`).join("");
        const body = rows
          .map((r) => `<tr>${r.map((c, i) => `<td data-label="${labels[i]}"${align(c.align)}>${this.parser.parseInline(c.tokens)}</td>`).join("")}</tr>`)
          .join("\n");
        return `<div class="table-wrap"><table>\n<thead><tr>${head}</tr></thead>\n<tbody>${body}</tbody>\n</table></div>\n`;
      },
    },
  });
  const html = md.parse(doc.markdown, { async: false }) as string;
  return { html, headings };
}

/** The markdown served at /docs/<slug>/llms.txt. */
export const llmsText = (doc: Doc) => `# ${doc.title}\n\n${doc.markdown}`;

/** /llms.txt (or /docs/<minor>/llms.txt): what kipitiny is and where each document is. */
export function llmsIndex(set: DocSet, origin = "") {
  let s =
    "# kipitiny\n\n> Lightweight self-hosted PaaS: Docker apps and databases with restore-tested backups, in one small Go process. Each document below is plain markdown.\n\n";
  if (set.version) s += `These are the docs of kipitiny ${set.version}.x; the latest are at ${origin}/llms.txt.\n\n`;
  s += "## Docs\n\n";
  for (const d of set.docs) s += `- [${d.title}](${origin}${set.base}/${d.slug}/llms.txt): ${d.description}\n`;
  if (!set.version && versions.length) {
    s += "\n## Versions\n\nThe docs of a given release, if it differs from the latest:\n\n";
    for (const v of versions) s += `- [${v.version}](${origin}${v.base}/llms.txt)\n`;
  }
  return s;
}
