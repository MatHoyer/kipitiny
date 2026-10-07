import { NavLink, Outlet } from "react-router";
import { Footer, Header } from "~/components/chrome";
import { docLinks } from "~/lib/docs.server";
import type { Route } from "./+types/docs-layout";
import "~/styles/docs.css";

export const loader = () => ({ docs: docLinks });

export default function DocsLayout({ loaderData }: Route.ComponentProps) {
  return (
    <div className="wrap wide">
      <Header />
      <div className="docs">
        <nav className="docs-nav" aria-label="Documentation">
          <NavLink to="/docs" end className="docs-nav-home">
            Overview
          </NavLink>
          <ol>
            {loaderData.docs.map((d) => (
              <li key={d.slug}>
                <NavLink to={`/docs/${d.slug}`}>{d.title}</NavLink>
              </li>
            ))}
          </ol>
        </nav>
        <main className="docs-main">
          <Outlet />
        </main>
      </div>
      <Footer />
    </div>
  );
}
