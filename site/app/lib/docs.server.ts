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

const files = import.meta.glob<string>("../../docs/*.md", {
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

export const docs: Doc[] = Object.entries(files)
  .map(([path, raw]) => parse(path, raw))
  .sort((a, b) => a.order - b.order);

export const docLinks: DocLink[] = docs.map(({ slug, title, description }) => ({ slug, title, description }));

export const findDoc = (slug: string | undefined) => docs.find((d) => d.slug === slug);

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
    },
  });
  const html = (md.parse(doc.markdown, { async: false }) as string)
    .replaceAll("<table>", '<div class="table-wrap"><table>')
    .replaceAll("</table>", "</table></div>");
  return { html, headings };
}

/** The markdown served at /docs/<slug>/llms.txt. */
export const llmsText = (doc: Doc) => `# ${doc.title}\n\n${doc.markdown}`;

/** /llms.txt: what kipitiny is and where each document is. */
export function llmsIndex(origin = "") {
  let s =
    "# kipitiny\n\n> Lightweight self-hosted PaaS: Docker apps and databases with restore-tested backups, in one small Go process. Each document below is plain markdown.\n\n## Docs\n\n";
  for (const d of docs) s += `- [${d.title}](${origin}/docs/${d.slug}/llms.txt): ${d.description}\n`;
  return s;
}
