import { Sidebar, SidebarContent, SidebarFooter, SidebarGroup, SidebarGroupContent, SidebarGroupLabel, SidebarHeader, SidebarInset, SidebarMenu, SidebarMenuBadge, SidebarMenuButton, SidebarMenuItem, SidebarProvider, SidebarTrigger } from 'homelab-ui';
import { Database, FolderKanban, Server, Settings } from 'lucide-react';

const items = [
  { title: 'Projects', icon: FolderKanban, active: true, badge: '12' },
  { title: 'Servers', icon: Server },
  { title: 'Databases', icon: Database, badge: '3' },
];
export const AppShell = () => (
  <SidebarProvider className="min-h-[420px]">
    <Sidebar collapsible="none" className="border-r">
      <SidebarHeader className="px-4 py-3 font-display text-lg font-bold">homelab</SidebarHeader>
      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupLabel>Workspace</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              {items.map((i) => (
                <SidebarMenuItem key={i.title}>
                  <SidebarMenuButton isActive={i.active}><i.icon />{i.title}</SidebarMenuButton>
                  {i.badge && <SidebarMenuBadge>{i.badge}</SidebarMenuBadge>}
                </SidebarMenuItem>
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
      <SidebarFooter>
        <SidebarMenu><SidebarMenuItem><SidebarMenuButton><Settings />Settings</SidebarMenuButton></SidebarMenuItem></SidebarMenu>
      </SidebarFooter>
    </Sidebar>
    <SidebarInset className="p-6">
      <h1 className="text-xl font-semibold">Projects</h1>
      <p className="mt-1 text-sm text-muted-foreground">12 projects on 2 servers.</p>
    </SidebarInset>
  </SidebarProvider>
);
