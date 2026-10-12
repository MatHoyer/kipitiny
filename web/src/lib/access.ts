import { useQuery } from "@tanstack/react-query";
import { api, type Feature, type Perm } from "@/api";

/**
 * What the signed-in user may do, to hide what the server would refuse. While it loads everything is allowed:
 * the server checks anyway.
 */
export function useAccess() {
  const account = useQuery({ queryKey: ["account"], queryFn: api.account, staleTime: 60_000 });
  const a = account.data;
  const admin = !a || a.role === "admin";
  return {
    admin,
    /** Holds p in the project; any permission implies view. */
    can: (projectId: string | undefined, p: Perm) => {
      if (admin) return true;
      const ps = projectId ? a.grants[projectId] : undefined;
      return !!ps?.length && (p === "view" || ps.includes(p));
    },
    /** May see a server-level resource. */
    has: (f: Feature) => admin || a.features.includes(f),
  };
}
