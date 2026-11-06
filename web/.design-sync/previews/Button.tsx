import { Button } from 'homelab-ui';
import { Plus, Trash2, Settings } from 'lucide-react';

export const Variants = () => (
  <div className="flex flex-wrap items-center gap-2">
    <Button>Deploy</Button>
    <Button variant="outline">Cancel</Button>
    <Button variant="secondary">Duplicate</Button>
    <Button variant="ghost">Skip</Button>
    <Button variant="destructive">Delete</Button>
    <Button variant="link">Read the docs</Button>
  </div>
);
export const Sizes = () => (
  <div className="flex flex-wrap items-center gap-2">
    <Button size="xs">Extra small</Button>
    <Button size="sm">Small</Button>
    <Button>Default</Button>
    <Button size="lg">Large</Button>
  </div>
);
export const WithIcon = () => (
  <div className="flex flex-wrap items-center gap-2">
    <Button><Plus data-icon="inline-start" />New project</Button>
    <Button variant="destructive"><Trash2 data-icon="inline-start" />Remove</Button>
    <Button size="icon" variant="outline" title="Settings"><Settings /></Button>
  </div>
);
export const States = () => (
  <div className="flex flex-wrap items-center gap-2">
    <Button loading>Saving</Button>
    <Button disabled>Unavailable</Button>
  </div>
);
