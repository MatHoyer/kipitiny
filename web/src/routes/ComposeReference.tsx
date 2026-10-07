import { useQuery } from "@tanstack/react-query";
import { Download } from "lucide-react";
import { useEffect } from "react";
import { useLocation } from "react-router";
import { ErrorText, Loading } from "@/components/common";
import { PageBody, PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";

/** GitHub-style heading ids, so the reference's #links work. */
const slug = (text: string) =>
  text
    .toLowerCase()
    .replace(/<[^>]+>/g, "")
    .replace(/[^a-z0-9 _-]/g, "")
    .trim()
    .replace(/ /g, "-");

/** Renders the reference the manager serves at /llms.txt. Markdown is
 * parsed in a lazily loaded chunk; the source is the manager's own. */
async function loadReference() {
  const [res, { Marked }] = await Promise.all([fetch("/llms.txt"), import("marked")]);
  if (!res.ok) throw new Error(`reference: ${res.status} ${res.statusText}`);
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

export function ComposeReference() {
  const doc = useQuery({ queryKey: ["compose-reference"], queryFn: loadReference, staleTime: Infinity });
  const { hash } = useLocation();
  useEffect(() => {
    if (doc.data && hash) document.getElementById(decodeURIComponent(hash.slice(1)))?.scrollIntoView();
  }, [doc.data, hash]);
  return (
    <>
      <PageHeader
        crumbs={[{ label: "Compose reference" }]}
        actions={
          <Button size="sm" variant="outline" asChild>
            <a href="/llms.txt" download="kipitiny-compose.md">
              <Download data-icon="inline-start" />
              Markdown
            </a>
          </Button>
        }
      />
      <PageBody>
        {doc.error ? (
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
