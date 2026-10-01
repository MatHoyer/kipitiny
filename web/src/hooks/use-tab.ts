import { useSearchParams } from "react-router";

/** The active tab, kept in the URL (?tab=) so reloads and links keep it. */
export function useTab<T extends string>(tabs: readonly T[], fallback: T): [T, (tab: string) => void] {
  const [params, setParams] = useSearchParams();
  const current = params.get("tab");
  const tab = tabs.includes(current as T) ? (current as T) : fallback;
  const setTab = (next: string) =>
    setParams(
      (p) => {
        if (next === fallback) p.delete("tab");
        else p.set("tab", next);
        return p;
      },
      { replace: true },
    );
  return [tab, setTab];
}
