import { handle } from "./backend";

/** Routes the app's /api calls to the in-browser backend instead of the network. */
export function install() {
  const real = window.fetch.bind(window);
  window.fetch = async (input, init) => {
    const url = new URL(input instanceof Request ? input.url : String(input), location.href);
    if (!url.pathname.startsWith(`${import.meta.env.BASE_URL}api/`) && !url.pathname.startsWith("/api/")) return real(input, init);
    const method = (init?.method ?? (input instanceof Request ? input.method : "GET")).toUpperCase();
    const raw = init?.body;
    const body = typeof raw === "string" && raw ? JSON.parse(raw) : {};
    // A beat of latency, so loading states look like the real thing.
    await new Promise((r) => setTimeout(r, 60));
    return handle(method, url, body);
  };
}
