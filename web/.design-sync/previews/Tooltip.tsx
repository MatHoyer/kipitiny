import { Button, Tooltip, TooltipContent, TooltipTrigger } from 'homelab-ui';

export const Open = () => (
  <div className="flex justify-center pt-10">
    <Tooltip open>
      <TooltipTrigger asChild><Button variant="outline">Hover me</Button></TooltipTrigger>
      <TooltipContent>Last deployed 2 hours ago</TooltipContent>
    </Tooltip>
  </div>
);
