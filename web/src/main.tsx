import { MutationCache, QueryCache, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, RouterProvider } from "react-router";
import { ApiError } from "./api";
import "./index.css";
import { Layout } from "./routes/Layout";
import { Project } from "./routes/Project";
import { Projects } from "./routes/Projects";
import { Service } from "./routes/Service";

// A 401 anywhere means the session is gone: re-check auth to show the login.
const onError = (err: Error) => {
  if (err instanceof ApiError && err.status === 401) queryClient.invalidateQueries({ queryKey: ["auth"] });
};
const queryClient: QueryClient = new QueryClient({
  queryCache: new QueryCache({ onError }),
  mutationCache: new MutationCache({ onError }),
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
    ],
  },
]);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
);
