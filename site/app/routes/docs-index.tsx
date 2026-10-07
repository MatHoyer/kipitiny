import { Link } from "react-router";
import { docLinks, docSetFor } from "~/lib/docs.server";
import { seo } from "~/lib/site";
import type { Route } from "./+types/docs-index";

export const meta: Route.MetaFunction = ({ loaderData: d }) =>
  seo({
    title: d?.version ? `Documentation ${d.version} · kipitiny` : "Documentation · kipitiny",
    description: "How to install kipitiny, deploy apps and databases, and back them up.",
    path: "/docs",
  });

export const loader = ({ request }: Route.LoaderArgs) => {
  const set = docSetFor(request);
  return { version: set.version, base: set.base, docs: docLinks(set) };
};

export default function DocsIndex({ loaderData: d }: Route.ComponentProps) {
  return (
    <>
      <h1 className="doc-title">Documentation</h1>
      <p className="doc-lede">
        Everything kipitiny does, from the first <code>docker compose up</code> to restoring a backup on another
        server. Each page is also plain markdown for LLMs and agents: <a href={d.version ? `${d.base}/llms.txt` : "/llms.txt"}>{d.version ? `${d.base}/llms.txt` : "/llms.txt"}</a> lists them.
      </p>
      <dl className="doc-list">
        {d.docs.map((doc) => (
          <div key={doc.slug}>
            <dt>
              <Link to={`${d.base}/${doc.slug}`}>{doc.title}</Link>
            </dt>
            <dd>{doc.description}.</dd>
          </div>
        ))}
      </dl>
    </>
  );
}
