import { Button, Toaster } from 'homelab-ui';
import { useEffect } from 'react';
import { toast } from 'sonner';

export const Types = () => {
  useEffect(() => {
    toast.success('Deployed', { description: 'Billing API is live.', duration: Infinity });
    toast.error("Couldn't sign in", { description: 'Wrong username or password.', duration: Infinity });
  }, []);
  return (
    <div className="h-48">
      <Toaster expand />
      <Button variant="outline" onClick={() => toast('Saved')}>Show a toast</Button>
    </div>
  );
};
