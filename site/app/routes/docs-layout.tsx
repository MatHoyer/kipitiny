import { Link, NavLink, Outlet, useNavigate, useParams } from "react-router";
import { Footer, Header } from "~/components/chrome";
import { docLinks, docSetFor, latest, versions } from "~/lib/docs.server";
import { VERSION } from "~/lib/site";
import type { Route } from "./+types/docs-layout";
import "~/styles/docs.css";

export const loader = ({ request }: Route.LoaderArgs) => {
  const set = docSetFor(request);
  return {
    version: set.version,
    base: set.base,
    docs: docLinks(set),
    sets: [latest, ...versions].map((s) => ({ version: s.version, base: s.base, slugs: s.docs.map((d) => d.slug) })),
  };
};

/** The minor version the latest docs describe ("0.10"), from the repo's VERSION. */
const latestMinor = VERSION.split(".").slice(0, 2).join(".");

export default function DocsLayout({ loaderData: d }: Route.ComponentProps) {
  const { slug } = useParams();
  const navigate = useNavigate();
  /** The same page in another version, or that version's overview if it doesn't have it. */
  const pathIn = (s: (typeof d.sets)[number]) => (slug && s.slugs.includes(slug) ? `${s.base}/${slug}` : s.base);
  // The latest release's copy is the same as the latest docs: one entry for both.
  const current = d.version === latestMinor ? "" : d.version;
  const choices = d.sets.filter((s) => !s.version || s.version !== latestMinor);
  return (
    <div className="wrap wide">
      <Header />
      <div className="docs">
        <nav className="docs-nav" aria-label="Documentation">
          {choices.length > 1 && (
            <label className="docs-version">
              <span>Version</span>
              <select value={current} onChange={(e) => navigate(pathIn(choices.find((s) => s.version === e.target.value)!))}>
                {choices.map((s) => (
                  <option key={s.version} value={s.version}>
                    {s.version || (latestMinor ? `${latestMinor} (latest)` : "latest")}
                  </option>
                ))}
              </select>
            </label>
          )}
          <NavLink to={d.base} end className="docs-nav-home">
            Overview
          </NavLink>
          <ol>
            {d.docs.map((doc) => (
              <li key={doc.slug}>
                <NavLink to={`${d.base}/${doc.slug}`}>{doc.title}</NavLink>
              </li>
            ))}
          </ol>
        </nav>
        <main className="docs-main">
          {current && (
            <p className="docs-old">
              These are the docs of kipitiny {d.version}. <Link to={pathIn(d.sets[0])}>Read the latest</Link>.
            </p>
          )}
          <Outlet />
        </main>
      </div>
      <Footer />
    </div>
  );
}
