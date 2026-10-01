import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Box,
  ChevronRight,
  ChevronsUpDown,
  DatabaseBackup,
  FolderKanban,
  LoaderCircle,
  LogOut,
  Monitor,
  Moon,
  Settings,
  Sun,
  SunMoon,
} from "lucide-react";
import { useTheme } from "next-themes";
import { Collapsible } from "radix-ui";
import { useEffect, useRef, useState } from "react";
import { NavLink, Outlet, useLocation } from "react-router";
import { toast } from "sonner";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarInset,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
  SidebarProvider,
  useSidebar,
} from "@/components/ui/sidebar";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { api } from "../api";
import { Login, Setup } from "./Auth";
import { settingsPages } from "./settings";

export function Layout() {
  const auth = useQuery({ queryKey: ["auth"], queryFn: api.authState, staleTime: Infinity });

  if (auth.isPending) return null;
  if (auth.error) return <p className="p-8 text-sm text-destructive">{auth.error.message}</p>;
  if (auth.data.setupRequired) return <Setup />;
  if (!auth.data.user) return <Login />;
  return <App username={auth.data.user.username} />;
}

const nav = [
  { to: "/", label: "Projects", icon: FolderKanban },
  { to: "/backups", label: "Backups", icon: DatabaseBackup },
];

function App({ username }: { username: string }) {
  return (
    <SidebarProvider>
      <Sidebar variant="inset">
        <SidebarHeader>
          <div className="flex items-center gap-2 px-2 pt-1 font-semibold tracking-tight">
            <Box className="size-5" />
            kipitiny
          </div>
        </SidebarHeader>
        <SidebarContent>
          <SidebarGroup>
            <SidebarGroupContent>
              <MainNav />
            </SidebarGroupContent>
          </SidebarGroup>
          <ProjectsNav />
        </SidebarContent>
        <SidebarFooter>
          <UpdateNotice />
          <DockerStatus />
          <NavUser username={username} />
        </SidebarFooter>
      </Sidebar>
      {/* The inset variant adds an m-2 margin from md up, so the height gives it back. */}
      <SidebarInset className="h-svh overflow-y-auto md:h-[calc(100svh-1rem)]">
        <Outlet />
      </SidebarInset>
    </SidebarProvider>
  );
}

function MainNav() {
  const { pathname } = useLocation();
  const { setOpenMobile } = useSidebar();
  return (
    <SidebarMenu>
      {nav.map(({ to, label, icon: Icon }) => (
        <SidebarMenuItem key={to}>
          <SidebarMenuButton asChild isActive={to === "/" ? pathname === "/" : pathname.startsWith(to)}>
            <NavLink to={to} onClick={() => setOpenMobile(false)}>
              <Icon />
              {label}
            </NavLink>
          </SidebarMenuButton>
        </SidebarMenuItem>
      ))}
      <SettingsNav />
    </SidebarMenu>
  );
}

/** Settings folds open in the sidebar, one entry per settings page. */
function SettingsNav() {
  const { pathname } = useLocation();
  const { setOpenMobile } = useSidebar();
  const inSettings = pathname.startsWith("/settings");
  const [open, setOpen] = useState(inSettings);
  useEffect(() => {
    if (inSettings) setOpen(true);
  }, [inSettings]);

  return (
    <Collapsible.Root asChild open={open} onOpenChange={setOpen}>
      <SidebarMenuItem className="group/collapsible">
        <Collapsible.Trigger asChild>
          <SidebarMenuButton>
            <Settings />
            Settings
            <ChevronRight className="ml-auto transition-transform group-data-[state=open]/collapsible:rotate-90" />
          </SidebarMenuButton>
        </Collapsible.Trigger>
        <Collapsible.Content>
          <SidebarMenuSub>
            {settingsPages.map(({ slug, label, icon: Icon }) => (
              <SidebarMenuSubItem key={slug}>
                <SidebarMenuSubButton asChild isActive={pathname === `/settings/${slug}`}>
                  <NavLink to={`/settings/${slug}`} onClick={() => setOpenMobile(false)}>
                    <Icon />
                    <span>{label}</span>
                  </NavLink>
                </SidebarMenuSubButton>
              </SidebarMenuSubItem>
            ))}
          </SidebarMenuSub>
        </Collapsible.Content>
      </SidebarMenuItem>
    </Collapsible.Root>
  );
}

function ProjectsNav() {
  const { pathname } = useLocation();
  const { setOpenMobile } = useSidebar();
  const projects = useQuery({ queryKey: ["projects"], queryFn: api.projects });
  if (!projects.data?.length) return null;
  return (
    <SidebarGroup>
      <SidebarGroupLabel>Projects</SidebarGroupLabel>
      <SidebarGroupContent>
        <SidebarMenu>
          {projects.data.map((p) => (
            <SidebarMenuItem key={p.id}>
              <SidebarMenuButton asChild size="sm" isActive={pathname === `/projects/${p.id}`}>
                <NavLink to={`/projects/${p.id}`} onClick={() => setOpenMobile(false)}>
                  <span className="truncate">{p.name}</span>
                </NavLink>
              </SidebarMenuButton>
            </SidebarMenuItem>
          ))}
        </SidebarMenu>
      </SidebarGroupContent>
    </SidebarGroup>
  );
}

const UPDATE_GIVE_UP_MS = 10 * 60_000;

/** Offers a newer published version and follows the manager's restart. */
function UpdateNotice() {
  const status = useQuery({ queryKey: ["status"], queryFn: api.status, refetchInterval: 15_000 });
  const update = status.data?.update;
  const [waiting, setWaiting] = useState(false);
  const from = useRef("");
  const apply = useMutation({
    meta: { error: "Couldn't start the update" },
    mutationFn: api.applyUpdate,
    onSuccess: (u) => {
      from.current = u.current;
      setWaiting(true);
    },
  });
  const updating = waiting || !!update?.updating;

  // The manager goes away while it's replaced: reload once another version
  // answers. The same version answering after an outage means the new one
  // failed its health check and the previous one was restored.
  useEffect(() => {
    if (!updating) return;
    const start = Date.now();
    const version = from.current || update?.current;
    let wentDown = false;
    const stop = (message: string) => {
      setWaiting(false);
      toast.error(message, { description: "The manager's log has the details." });
    };
    const id = setInterval(async () => {
      try {
        const s = await api.status();
        if (s.version !== version) return window.location.reload();
        if (wentDown) stop("The update failed; the previous version was restored");
        else if (!s.update.updating && s.update.error) setWaiting(false);
      } catch {
        wentDown = true;
      }
      if (Date.now() - start > UPDATE_GIVE_UP_MS) stop("The update is taking too long");
    }, 3000);
    return () => clearInterval(id);
  }, [updating]); // runs per update attempt, not per status refresh

  if (!update || (!update.available && !updating)) return null;
  return (
    <div className="mx-2 rounded-lg border bg-sidebar-accent/50 p-3 text-xs">
      {updating ? (
        <div className="flex items-center gap-2">
          <LoaderCircle className="size-3.5 shrink-0 animate-spin" />
          <span>Updating to {update.latest}… The page reloads when it's done.</span>
        </div>
      ) : (
        <>
          <div className="text-sm font-medium">Update available</div>
          <div className="text-muted-foreground">
            {update.current} → {update.latest} ·{" "}
            <a
              className="underline underline-offset-2"
              href={`https://github.com/MatHoyer/kipitiny/releases/tag/${update.latest}`}
              target="_blank"
              rel="noreferrer"
            >
              notes
            </a>
          </div>
          {update.canApply ? (
            <ConfirmDialog
              trigger={
                <Button size="sm" className="mt-2 w-full" disabled={apply.isPending}>
                  Update now
                </Button>
              }
              title={`Update to ${update.latest}?`}
              description="The manager restarts on the new version. Apps, databases and Traefik keep running, and running deploys finish first. If the new version doesn't start, the previous one is restored."
              confirmLabel="Update"
              destructive={false}
              onConfirm={() => apply.mutate()}
            />
          ) : (
            <div className="mt-1 text-muted-foreground">{update.reason}</div>
          )}
          {update.error && <div className="mt-1 text-destructive">{update.error}</div>}
        </>
      )}
    </div>
  );
}

function DockerStatus() {
  const status = useQuery({ queryKey: ["status"], queryFn: api.status, refetchInterval: 15_000 });
  const docker = status.data?.docker;
  return (
    <div className="flex items-center gap-2 px-2 text-xs text-muted-foreground" title={status.data?.dockerError}>
      <span className={cn("size-2 shrink-0 rounded-full", docker ? "bg-emerald-500" : "bg-red-500")} />
      <span className="truncate">
        {status.data && `kipitiny ${status.data.version} · `}
        {docker ? `Docker ${docker.version}` : "Docker unreachable"}
      </span>
    </div>
  );
}

const themes = [
  { value: "light", label: "Light", icon: Sun },
  { value: "dark", label: "Dark", icon: Moon },
  { value: "system", label: "System", icon: Monitor },
];

function NavUser({ username }: { username: string }) {
  const qc = useQueryClient();
  const { isMobile } = useSidebar();
  const { theme, setTheme } = useTheme();
  const logout = useMutation({ meta: { error: "Couldn't sign out" }, mutationFn: api.logout, onSettled: () => qc.resetQueries() });
  const avatar = (
    <span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-sidebar-primary text-sm font-semibold text-sidebar-primary-foreground uppercase">
      {username.slice(0, 1)}
    </span>
  );

  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <SidebarMenuButton
              size="lg"
              className="data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground"
            >
              {avatar}
              <span className="flex-1 truncate text-left text-sm font-medium">{username}</span>
              <ChevronsUpDown className="ml-auto size-4" />
            </SidebarMenuButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent
            className="w-(--radix-dropdown-menu-trigger-width) min-w-56"
            side={isMobile ? "bottom" : "right"}
            align="end"
            sideOffset={4}
          >
            <DropdownMenuLabel className="p-0 font-normal">
              <div className="flex items-center gap-2 px-1 py-1.5 text-sm">
                {avatar}
                <span className="truncate font-medium">{username}</span>
              </div>
            </DropdownMenuLabel>
            <DropdownMenuSeparator />
            <div className="flex items-center gap-2 px-2 py-1 text-sm">
              <SunMoon className="size-4 text-muted-foreground" />
              Theme
              <ToggleGroup
                type="single"
                size="sm"
                variant="outline"
                spacing={0}
                value={theme}
                onValueChange={(v) => v && setTheme(v)}
                aria-label="Theme"
                className="ml-auto"
              >
                {themes.map(({ value, label, icon: Icon }) => (
                  <Tooltip key={value}>
                    <TooltipTrigger asChild>
                      {/* The tooltip trigger overwrites data-state, so selection is styled from aria-checked. */}
                      <ToggleGroupItem value={value} aria-label={label} className="aria-checked:bg-muted">
                        <Icon />
                      </ToggleGroupItem>
                    </TooltipTrigger>
                    <TooltipContent>{label}</TooltipContent>
                  </Tooltip>
                ))}
              </ToggleGroup>
            </div>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" className="gap-2 px-2 py-2" onClick={() => logout.mutate()}>
              <LogOut />
              Sign out
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </SidebarMenuItem>
    </SidebarMenu>
  );
}
