import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Box,
  ChevronsUpDown,
  DatabaseBackup,
  FolderKanban,
  LogOut,
  Monitor,
  Moon,
  Settings,
  Sun,
  SunMoon,
} from "lucide-react";
import { useTheme } from "next-themes";
import { NavLink, Outlet, useLocation } from "react-router";
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
  SidebarProvider,
  useSidebar,
} from "@/components/ui/sidebar";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { api } from "../api";
import { Login, Setup } from "./Auth";

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
  { to: "/settings", label: "Settings", icon: Settings },
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
    </SidebarMenu>
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

function DockerStatus() {
  const status = useQuery({ queryKey: ["status"], queryFn: api.status, refetchInterval: 15_000 });
  const docker = status.data?.docker;
  return (
    <div className="flex items-center gap-2 px-2 text-xs text-muted-foreground" title={status.data?.dockerError}>
      <span className={cn("size-2 shrink-0 rounded-full", docker ? "bg-emerald-500" : "bg-red-500")} />
      <span className="truncate">{docker ? `Docker ${docker.version}` : "Docker unreachable"}</span>
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
  const logout = useMutation({ mutationFn: api.logout, onSettled: () => qc.resetQueries() });
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
