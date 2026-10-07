import { useQuery } from "@tanstack/react-query";
import { Download } from "lucide-react";
import { useEffect } from "react";
import { useLocation, useParams } from "react-router";
import { ErrorText, Loading } from "@/components/common";
import { PageBody, PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { docs } from "./settings/Docs";

/** GitHub-style heading ids, so the reference's #links work. */
const slug = (text: string) =>
  text
    .toLowerCase()
    .replace(/<[^>]+>/g, "")
    .replace(/[^a-z0-9 _-]/g, "")
    .trim()
    .replace(/ /g, "-");

/** Renders a document the manager serves. Markdown is parsed in a lazily
 * loaded chunk; the source is the manager's own. */
async function loadDoc(source: string) {
  const [res, { Marked }] = await Promise.all([fetch(source), import("marked")]);
  if (!res.ok) throw new Error(`${source}: ${res.status} ${res.statusText}`);
  const md = new Marked({
    renderer: {
      heading({ tokens, depth }) {
        const html = this.parser.parseInline(tokens);
        return `<h${depth} id="${slug(html)}">${html}</h${depth}>\n`;
      },
    },
  });
  return md.parse(await res.text());
}

/** One document of the Docs page. */
export function DocPage() {
  const { slug = "" } = useParams();
  const entry = docs.find((d) => d.slug === slug);
  const doc = useQuery({
    queryKey: ["doc", slug],
    queryFn: () => loadDoc(entry!.source),
    enabled: !!entry,
    staleTime: Infinity,
  });
  const { hash } = useLocation();
  useEffect(() => {
    if (doc.data && hash) document.getElementById(decodeURIComponent(hash.slice(1)))?.scrollIntoView();
  }, [doc.data, hash]);
  const crumbs = [{ label: "Docs", to: "/docs" }, { label: entry?.title ?? slug }];
  return (
    <>
      <PageHeader
        crumbs={crumbs}
        actions={
          entry && (
            <Button size="sm" variant="outline" asChild>
              <a href={entry.source} download={`kipitiny-${entry.slug}.md`}>
                <Download data-icon="inline-start" />
                Markdown
              </a>
            </Button>
          )
        }
      />
      <PageBody>
        {!entry ? (
          <p className="text-sm text-muted-foreground">No such document.</p>
        ) : doc.error ? (
          <ErrorText error={doc.error} />
        ) : !doc.data ? (
          <Loading />
        ) : (
          <article className="doc max-w-4xl" dangerouslySetInnerHTML={{ __html: doc.data }} />
        )}
      </PageBody>
    </>
  );
}
