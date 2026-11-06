import { Separator } from 'homelab-ui';

export const Horizontal = () => (
  <div className="w-72 text-sm">
    <p className="font-medium">Environment</p>
    <Separator className="my-3" />
    <p className="text-muted-foreground">12 variables, 3 secrets.</p>
  </div>
);
export const Vertical = () => (
  <div className="flex h-5 items-center gap-3 text-sm">
    <span>Logs</span><Separator orientation="vertical" /><span>Metrics</span><Separator orientation="vertical" /><span>Settings</span>
  </div>
);
