import { Button, Spinner } from 'homelab-ui';

export const Default = () => <Spinner />;
export const Sizes = () => <div className="flex items-center gap-3"><Spinner className="size-3" /><Spinner /><Spinner className="size-6" /></div>;
export const InContext = () => (
  <div className="flex items-center gap-3 text-sm text-muted-foreground"><Spinner />Waiting for the first deploy<Button size="sm" loading>Retrying</Button></div>
);
