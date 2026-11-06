import { Button, Collapsible, CollapsibleContent, CollapsibleTrigger } from 'homelab-ui';
import { ChevronDown } from 'lucide-react';

export const Open = () => (
  <Collapsible defaultOpen className="w-80 space-y-2 text-sm">
    <CollapsibleTrigger asChild><Button variant="ghost" className="w-full justify-between">Advanced settings<ChevronDown /></Button></CollapsibleTrigger>
    <CollapsibleContent className="rounded-lg border p-3 text-muted-foreground">Memory limit: 512 MB. Restart policy: always.</CollapsibleContent>
  </Collapsible>
);
export const Closed = () => (
  <Collapsible className="w-80 text-sm">
    <CollapsibleTrigger asChild><Button variant="ghost" className="w-full justify-between">Advanced settings<ChevronDown /></Button></CollapsibleTrigger>
    <CollapsibleContent className="p-3">Hidden until opened.</CollapsibleContent>
  </Collapsible>
);
