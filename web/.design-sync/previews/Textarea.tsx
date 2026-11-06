import { Label, Textarea } from 'homelab-ui';

export const Default = () => (
  <div className="grid w-80 gap-2">
    <Label htmlFor="notes">Release notes</Label>
    <Textarea id="notes" defaultValue={"Fixes the login redirect.\nAdds passkey support."} />
  </div>
);
export const Invalid = () => <Textarea className="w-80" aria-invalid="true" defaultValue="Too short" />;
export const Disabled = () => <Textarea className="w-80" disabled defaultValue="Read-only description" />;
