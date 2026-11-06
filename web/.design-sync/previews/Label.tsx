import { Checkbox, Input, Label } from 'homelab-ui';

export const WithInput = () => (
  <div className="grid w-72 gap-2">
    <Label htmlFor="name">Project name</Label>
    <Input id="name" defaultValue="Billing" />
  </div>
);
export const WithCheckbox = () => (
  <div className="flex items-center gap-2">
    <Checkbox id="backups" defaultChecked />
    <Label htmlFor="backups">Enable daily backups</Label>
  </div>
);
