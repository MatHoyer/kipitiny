import { Input, Label } from 'homelab-ui';

export const Default = () => (
  <div className="grid w-72 gap-2">
    <Label htmlFor="domain">Domain</Label>
    <Input id="domain" placeholder="app.example.com" />
  </div>
);
export const Invalid = () => (
  <div className="grid w-72 gap-2">
    <Label htmlFor="port">Port</Label>
    <Input id="port" defaultValue="99999" aria-invalid="true" />
  </div>
);
export const Disabled = () => <Input className="w-72" defaultValue="Managed by the server" disabled />;
