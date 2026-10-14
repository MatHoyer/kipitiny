import { api } from "@/api";

/**
 * Reads a value the single sign-on callback put in the URL (`?sso-error=…`, `#sso-mfa=…`) and removes it, so a
 * reload doesn't show it again.
 */
export function takeSSOParam(name: string): string | null {
  const url = new URL(window.location.href);
  const hash = new URLSearchParams(url.hash.slice(1));
  let value = url.searchParams.get(name);
  if (value !== null) url.searchParams.delete(name);
  else if ((value = hash.get(name)) !== null) {
    hash.delete(name);
    url.hash = hash.toString();
  } else return null;
  window.history.replaceState(window.history.state, "", url);
  return value;
}

/** Leaves for the provider's sign-in page; it sends the browser back to this page. Never resolves: the page unloads. */
export async function signInWithSSO(id: string): Promise<never> {
  const { url } = await api.beginSSO(id, window.location.pathname + window.location.search);
  return leaveFor(url);
}

export function leaveFor(url: string): Promise<never> {
  window.location.assign(url);
  return new Promise(() => {});
}
