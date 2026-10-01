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
import { friendlyError } from "./lib/errors";
import { Backups } from "./routes/Backups";
import { Layout } from "./routes/Layout";
import { Project } from "./routes/Project";
import { Projects } from "./routes/Projects";
import { Service } from "./routes/Service";
import { SettingsShell } from "./routes/settings";
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
      { index: true, element: <Projects /> },
      { path: "projects/:id", element: <Project /> },
      { path: "services/:id", element: <Service /> },
      { path: "backups", element: <Backups /> },
      { path: "settings", element: <Navigate to="/settings/servers" replace /> },
      { path: "settings/:page", element: <SettingsShell /> },
      { path: "settings/servers/:id", element: <ServerPage /> },
    ],
  },
]);

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
