import { Button, DropdownMenu, DropdownMenuCheckboxItem, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuShortcut, DropdownMenuTrigger } from 'homelab-ui';
import { MoreHorizontal, Pencil, Trash2 } from 'lucide-react';

export const Open = () => (
  <div className="h-64">
    <DropdownMenu open modal={false}>
      <DropdownMenuTrigger asChild><Button variant="outline"><MoreHorizontal data-icon="inline-start" />Actions</Button></DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-52">
        <DropdownMenuLabel>Billing API</DropdownMenuLabel>
        <DropdownMenuItem><Pencil />Rename<DropdownMenuShortcut>R</DropdownMenuShortcut></DropdownMenuItem>
        <DropdownMenuCheckboxItem checked>Auto deploy</DropdownMenuCheckboxItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem variant="destructive"><Trash2 />Delete</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  </div>
);
