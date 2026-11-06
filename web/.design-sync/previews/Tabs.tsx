import { Tabs, TabsContent, TabsList, TabsTrigger } from 'homelab-ui';

export const Default = () => (
  <Tabs defaultValue="logs" className="w-96">
    <TabsList>
      <TabsTrigger value="overview">Overview</TabsTrigger>
      <TabsTrigger value="logs">Logs</TabsTrigger>
      <TabsTrigger value="settings">Settings</TabsTrigger>
    </TabsList>
    <TabsContent value="overview" className="text-sm">Service health at a glance.</TabsContent>
    <TabsContent value="logs" className="text-sm text-muted-foreground">Streaming the last 200 lines.</TabsContent>
    <TabsContent value="settings" className="text-sm">Domains, environment, and limits.</TabsContent>
  </Tabs>
);
export const Line = () => (
  <Tabs defaultValue="deploys" className="w-96">
    <TabsList variant="line">
      <TabsTrigger value="deploys">Deployments</TabsTrigger>
      <TabsTrigger value="backups">Backups</TabsTrigger>
    </TabsList>
    <TabsContent value="deploys" className="text-sm text-muted-foreground">3 deployments this week.</TabsContent>
    <TabsContent value="backups" className="text-sm">Daily at 02:00.</TabsContent>
  </Tabs>
);
