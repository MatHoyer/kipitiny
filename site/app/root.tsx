import { isRouteErrorResponse, Links, Meta, Outlet, Scripts, ScrollRestoration } from "react-router";
import type { Route } from "./+types/root";
import { Footer, Header } from "./components/chrome";
import "./styles/site.css";

export const links: Route.LinksFunction = () => [{ rel: "icon", href: "/favicon.svg", type: "image/svg+xml" }];

export function Layout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <head>
        <meta charSet="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <Meta />
        <Links />
      </head>
      <body>
        {children}
        <ScrollRestoration />
        <Scripts />
      </body>
    </html>
  );
}

export default function App() {
  return <Outlet />;
}

export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  const notFound = isRouteErrorResponse(error) && error.status === 404;
  return (
    <div className="wrap">
      <Header />
      <main className="error-page">
        <h1>{notFound ? "This page doesn't exist." : "Something went wrong."}</h1>
        <p>
          {notFound ? "The link may be old. " : ""}
          Go to the <a href="/">home page</a> or the <a href="/docs">documentation</a>.
        </p>
      </main>
      <Footer />
    </div>
  );
}
