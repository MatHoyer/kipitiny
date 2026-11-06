import { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue } from 'homelab-ui';

const Items = () => (
  <SelectContent>
    <SelectGroup>
      <SelectLabel>Databases</SelectLabel>
      <SelectItem value="pg">PostgreSQL</SelectItem>
      <SelectItem value="mysql">MySQL</SelectItem>
      <SelectItem value="redis">Redis</SelectItem>
    </SelectGroup>
  </SelectContent>
);
export const Closed = () => (
  <Select defaultValue="pg"><SelectTrigger className="w-56"><SelectValue /></SelectTrigger><Items /></Select>
);
export const Placeholder = () => (
  <Select><SelectTrigger className="w-56"><SelectValue placeholder="Pick a database" /></SelectTrigger><Items /></Select>
);
export const Open = () => (
  <div className="h-56 w-56">
    <Select defaultValue="mysql" open><SelectTrigger className="w-56"><SelectValue /></SelectTrigger><Items /></Select>
  </div>
);
