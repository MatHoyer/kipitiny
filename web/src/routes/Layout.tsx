import { useQuery } from "@tanstack/react-query";
import { Link, Outlet } from "react-router";
import { api } from "../api";

export function Layout() {
  const status = useQuery({ queryKey: ["status"], queryFn: api.status, refetchInterval: 15_000 });
  const docker = status.data?.docker;

  return (
    <div className="min-h-screen bg-zinc-50 text-zinc-900 dark:bg-zinc-950 dark:text-zinc-100">
      <header className="border-b border-zinc-200 dark:border-zinc-800">
        <div className="mx-auto flex max-w-5xl items-center justify-between px-4 py-3">
          <Link to="/" className="font-semibold tracking-tight">
            kipitiny
          </Link>
          <span className="flex items-center gap-2 text-xs text-zinc-500">
            <span className={`size-2 rounded-full ${docker ? "bg-emerald-500" : "bg-red-500"}`} />
            {docker ? `Docker ${docker.version}` : (status.data?.dockerError ?? "Docker unreachable")}
          </span>
        </div>
      </header>
      <main className="mx-auto max-w-5xl px-4 py-8">
        <Outlet />
      </main>
    </div>
  );
}
