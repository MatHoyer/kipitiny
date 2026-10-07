import { useEffect } from "react";
import { Link, useLocation } from "react-router";
import { docs, findDoc, render } from "~/lib/docs.server";
import { seo } from "~/lib/site";
import type { Route } from "./+types/doc";

export function loader({ params }: Route.LoaderArgs) {
  const doc = findDoc(params.slug);
  if (!doc) throw new Response("Not found", { status: 404 });
  const i = docs.indexOf(doc);
  const link = (d?: (typeof docs)[number]) => d && { slug: d.slug, title: d.title };
  return {
    slug: doc.slug,
    title: doc.title,
    description: doc.description,
    ...render(doc),
    prev: link(docs[i - 1]),
    next: link(docs[i + 1]),
  };
}

export const meta: Route.MetaFunction = ({ loaderData: d }) =>
  d ? seo({ title: `${d.title} · kipitiny`, description: `${d.description}.`, path: `/docs/${d.slug}` }) : [];

export default function DocPage({ loaderData: d }: Route.ComponentProps) {
  const { hash } = useLocation();
  const hasToc = d.headings.length > 1;
  useEffect(() => {
    if (hash) document.getElementById(decodeURIComponent(hash.slice(1)))?.scrollIntoView();
  }, [hash, d.slug]);
  return (
    <div className={hasToc ? "doc-grid" : undefined}>
      <article>
        <h1 className="doc-title">{d.title}</h1>
        <p className="doc-lede">{d.description}.</p>
        <p className="doc-raw">
          <a href={`/docs/${d.slug}/llms.txt`}>Markdown for LLMs</a>
        </p>
        <div className="prose" dangerouslySetInnerHTML={{ __html: d.html }} />
        <nav className="pager" aria-label="Previous and next">
          {d.prev ? (
            <Link to={`/docs/${d.prev.slug}`} className="prev">
              <small>Previous</small>
              {d.prev.title}
            </Link>
          ) : (
            <span />
          )}
          {d.next && (
            <Link to={`/docs/${d.next.slug}`} className="next">
              <small>Next</small>
              {d.next.title}
            </Link>
          )}
        </nav>
      </article>
      {hasToc && (
        <aside className="toc">
          <p>On this page</p>
          <ul>
            {d.headings.map((h) => (
              <li key={h.id}>
                <a href={`#${h.id}`}>{h.text}</a>
              </li>
            ))}
          </ul>
        </aside>
      )}
    </div>
  );
}
