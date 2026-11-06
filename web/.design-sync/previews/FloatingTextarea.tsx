import { FloatingTextarea } from 'homelab-ui';

export const Empty = () => <FloatingTextarea className="w-80" label="Description" />;
export const Filled = () => (
  <FloatingTextarea className="w-80" label="Description" defaultValue="Nightly job that prunes old backups and reports the space it freed." />
);
