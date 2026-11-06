import { FloatingInput } from 'homelab-ui';
import { Search } from 'lucide-react';

export const Empty = () => <FloatingInput className="w-80" label="Username" />;
export const Filled = () => <FloatingInput className="w-80" label="Email" defaultValue="ada@example.com" />;
export const Invalid = () => (
  <FloatingInput className="w-80" label="Password" type="password" defaultValue="hunter2" aria-invalid="true" />
);
export const WithHelp = () => (
  <FloatingInput className="w-80" label="Domain" defaultValue="app.example.com" description="Point its DNS at this server first." />
);
export const SmallWithIcon = () => <FloatingInput className="w-64" size="sm" label="Search projects" icon={<Search />} />;
