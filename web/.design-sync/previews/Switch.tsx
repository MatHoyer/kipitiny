import { Label, Switch } from 'homelab-ui';

export const States = () => (
  <div className="grid gap-3">
    <div className="flex items-center gap-2"><Switch id="on" defaultChecked /><Label htmlFor="on">Automatic deploys</Label></div>
    <div className="flex items-center gap-2"><Switch id="off" /><Label htmlFor="off">Maintenance mode</Label></div>
    <div className="flex items-center gap-2"><Switch id="dis" disabled /><Label htmlFor="dis">Managed by your team</Label></div>
  </div>
);
export const Small = () => <Switch size="sm" defaultChecked aria-label="Compact toggle" />;
