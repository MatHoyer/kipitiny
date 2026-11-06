import { Button, Tip } from 'homelab-ui';

export const Default = () => (
  <div className="flex justify-center pt-10">
    <Tip content="Restarts every service"><Button variant="outline" autoFocus>Restart</Button></Tip>
  </div>
);
