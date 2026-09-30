import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, NavLink, Outlet } from "react-router";
import { api } from "../api";
import { Login, Setup } from "./Auth";

export function Layout() {
  const auth = useQuery({ queryKey: ["auth"], queryFn: api.authState, staleTime: Infinity });

  if (auth.isPending) return null;
  if (auth.error) return <p className="p-8 text-sm text-red-600">{auth.error.message}</p>;
  if (auth.data.setupRequired) return <Setup />;
  if (!auth.data.user) return <Login />;
  return <App username={auth.data.user.username} />;
}

function App({ username }: { username: string }) {
  const qc = useQueryClient();
  const status = useQuery({ queryKey: ["status"], queryFn: api.status, refetchInterval: 15_000 });
  const logout = useMutation({ mutationFn: api.logout, onSettled: () => qc.resetQueries() });
  const docker = status.data?.docker;

  return (
    <div className="min-h-screen bg-zinc-50 text-zinc-900 dark:bg-zinc-950 dark:text-zinc-100">
      <header className="border-b border-zinc-200 dark:border-zinc-800">
        <div className="mx-auto flex max-w-5xl items-center justify-between gap-4 px-4 py-3">
          <nav className="flex items-center gap-6 text-sm">
            <Link to="/" className="font-semibold tracking-tight">
              kipitiny
            </Link>
            {[
              ["/", "Projects"],
              ["/backups", "Backups"],
              ["/settings", "Settings"],
            ].map(([to, label]) => (
              <NavLink
                key={to}
                to={to}
                end={to === "/"}
                className={({ isActive }) =>
                  isActive ? "text-zinc-900 dark:text-zinc-100" : "text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100"
                }
              >
                {label}
              </NavLink>
            ))}
          </nav>
          <div className="flex items-center gap-4 text-xs text-zinc-500">
            <span className="flex items-center gap-2">
              <span className={`size-2 rounded-full ${docker ? "bg-emerald-500" : "bg-red-500"}`} />
              {docker ? `Docker ${docker.version}` : (status.data?.dockerError ?? "Docker unreachable")}
            </span>
            <span>{username}</span>
            <button onClick={() => logout.mutate()} className="hover:text-zinc-900 dark:hover:text-zinc-100">
              Sign out
            </button>
          </div>
        </div>
      </header>
      <main className="mx-auto max-w-5xl px-4 py-8">
        <Outlet />
      </main>
    </div>
  );
}
