import { useSearchParams } from "react-router";

/** The active tab, kept in the URL (?tab=, or another param for nested tabs) so reloads and links keep it. */
export function useTab<T extends string>(tabs: readonly T[], fallback: T, param = "tab"): [T, (tab: string) => void] {
  const [params, setParams] = useSearchParams();
  const current = params.get(param);
  const tab = tabs.includes(current as T) ? (current as T) : fallback;
  const setTab = (next: string) =>
    setParams(
      (p) => {
        if (next === fallback) p.delete(param);
        else p.set(param, next);
        return p;
      },
      { replace: true },
    );
  return [tab, setTab];
}
