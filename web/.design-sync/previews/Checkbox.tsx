import { Checkbox, Label } from 'homelab-ui';

export const States = () => (
  <div className="grid gap-3">
    <div className="flex items-center gap-2"><Checkbox id="a" /><Label htmlFor="a">Send alerts</Label></div>
    <div className="flex items-center gap-2"><Checkbox id="b" defaultChecked /><Label htmlFor="b">Keep logs for 30 days</Label></div>
    <div className="flex items-center gap-2"><Checkbox id="c" disabled /><Label htmlFor="c">Unavailable on this plan</Label></div>
    <div className="flex items-center gap-2"><Checkbox id="d" aria-invalid="true" /><Label htmlFor="d">Accept the terms</Label></div>
  </div>
);
