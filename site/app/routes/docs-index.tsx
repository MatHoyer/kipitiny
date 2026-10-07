import { Link } from "react-router";
import { docLinks } from "~/lib/docs.server";
import { seo } from "~/lib/site";
import type { Route } from "./+types/docs-index";

export const meta: Route.MetaFunction = () =>
  seo({
    title: "Documentation · kipitiny",
    description: "How to install kipitiny, deploy apps and databases, and back them up.",
    path: "/docs",
  });

export const loader = () => ({ docs: docLinks });

export default function DocsIndex({ loaderData }: Route.ComponentProps) {
  return (
    <>
      <h1 className="doc-title">Documentation</h1>
      <p className="doc-lede">
        Everything kipitiny does, from the first <code>docker compose up</code> to restoring a backup on another
        server. Each page is also plain markdown for LLMs and agents: <a href="/llms.txt">/llms.txt</a> lists them.
      </p>
      <dl className="doc-list">
        {loaderData.docs.map((d) => (
          <div key={d.slug}>
            <dt>
              <Link to={`/docs/${d.slug}`}>{d.title}</Link>
            </dt>
            <dd>{d.description}.</dd>
          </div>
        ))}
      </dl>
    </>
  );
}
