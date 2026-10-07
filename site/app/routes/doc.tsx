import { useEffect } from "react";
import { Link, useLocation } from "react-router";
import { docSetFor, findDoc, latest, render } from "~/lib/docs.server";
import { seo } from "~/lib/site";
import type { Route } from "./+types/doc";

export function loader({ request, params }: Route.LoaderArgs) {
  const set = docSetFor(request);
  const doc = findDoc(set, params.slug);
  if (!doc) throw new Response("Not found", { status: 404 });
  const i = set.docs.indexOf(doc);
  const link = (d?: (typeof set.docs)[number]) => d && { slug: d.slug, title: d.title };
  return {
    base: set.base,
    // A release's copy of a page points search engines to the latest one.
    canonical: findDoc(latest, doc.slug) ? `${latest.base}/${doc.slug}` : `${set.base}/${doc.slug}`,
    slug: doc.slug,
    title: doc.title,
    description: doc.description,
    ...render(doc),
    prev: link(set.docs[i - 1]),
    next: link(set.docs[i + 1]),
  };
}

export const meta: Route.MetaFunction = ({ loaderData: d }) =>
  d ? seo({ title: `${d.title} · kipitiny`, description: `${d.description}.`, path: d.canonical }) : [];

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
          <a href={`${d.base}/${d.slug}/llms.txt`}>Markdown for LLMs</a>
        </p>
        <div className="prose" dangerouslySetInnerHTML={{ __html: d.html }} />
        <nav className="pager" aria-label="Previous and next">
          {d.prev ? (
            <Link to={`${d.base}/${d.prev.slug}`} className="prev">
              <small>Previous</small>
              {d.prev.title}
            </Link>
          ) : (
            <span />
          )}
          {d.next && (
            <Link to={`${d.base}/${d.next.slug}`} className="next">
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
