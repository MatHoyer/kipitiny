import { MutationCache, QueryCache, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ThemeProvider } from "next-themes";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, Navigate, RouterProvider } from "react-router";
import { toast } from "sonner";
import { ApiError } from "./api";
import { Toaster } from "./components/ui/sonner";
import { TooltipProvider } from "./components/ui/tooltip";
import "./index.css";
import { DocsRedirect } from "./lib/docs";
import { friendlyError } from "./lib/errors";
import { AccountPage } from "./routes/Account";
import { Backup } from "./routes/Backup";
import { Backups } from "./routes/Backups";
import { Layout } from "./routes/Layout";
import { MapPage } from "./routes/MapPage";
import { Project } from "./routes/Project";
import { Projects } from "./routes/Projects";
import { Service } from "./routes/Service";
import { SettingsShell, settingsPages } from "./routes/settings";
import { ServerPage } from "./routes/settings/Servers";

// A 401 anywhere means the session is gone: re-check auth to show the login.
const onError = (err: Error) => {
  if (err instanceof ApiError && err.status === 401) queryClient.invalidateQueries({ queryKey: ["auth"] });
};
declare module "@tanstack/react-query" {
  interface Register {
    // error: the toast title when the mutation fails, e.g. "Couldn't save the schedule".
    // Set it to false when the caller reports the error itself.
    mutationMeta: { error?: string | false };
  }
}

const queryClient: QueryClient = new QueryClient({
  queryCache: new QueryCache({ onError }),
  mutationCache: new MutationCache({
    onError: (err, _vars, _ctx, mutation) => {
      onError(err);
      const title = mutation.meta?.error;
      if (title !== false) toast.error(title ?? "Something went wrong", { description: friendlyError(err), duration: 8000 });
    },
  }),
  defaultOptions: {
    queries: { retry: (n, err) => !(err instanceof ApiError && err.status < 500) && n < 2 },
  },
});

const router = createBrowserRouter([
  {
    path: "/",
    element: <Layout />,
    children: [
      { index: true, element: <MapPage /> },
      { path: "projects", element: <Projects /> },
      { path: "projects/:id", element: <Project /> },
      { path: "services/:id", element: <Service /> },
      // The map was here before it became the home page.
      { path: "map", element: <Navigate to="/" replace /> },
      { path: "docs/*", element: <DocsRedirect /> },
      { path: "account", element: <AccountPage /> },
      { path: "backups", element: <Backups /> },
      { path: "backups/:id", element: <Backup /> },
      ...settingsPages.filter((p) => p.page).map((p) => ({ path: p.slug, element: <SettingsShell slug={p.slug} /> })),
      { path: "servers/:id", element: <ServerPage /> },
    ],
  },
], {
  // "/" normally; "/demo" for the demo build the website embeds.
  basename: import.meta.env.BASE_URL.replace(/\/$/, "") || undefined,
});

const start = () =>
  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <ThemeProvider attribute="class" defaultTheme="system" enableSystem disableTransitionOnChange>
        <TooltipProvider>
          <QueryClientProvider client={queryClient}>
            <RouterProvider router={router} />
          </QueryClientProvider>
          <Toaster />
        </TooltipProvider>
      </ThemeProvider>
    </StrictMode>,
  );

// The demo build answers /api in the browser; normal builds drop this branch.
if (import.meta.env.MODE === "demo") import("./demo/install").then((m) => (m.install(), start()));
else start();
