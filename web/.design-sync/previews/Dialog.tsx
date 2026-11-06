import { Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, FloatingInput } from 'homelab-ui';

export const Open = () => (
  <Dialog defaultOpen>
    <DialogContent onOpenAutoFocus={(e) => e.preventDefault()}>
      <DialogHeader>
        <DialogTitle>Add a domain</DialogTitle>
        <DialogDescription>Point its DNS at this server, then add it here.</DialogDescription>
      </DialogHeader>
      <FloatingInput label="Domain" defaultValue="app.example.com" />
      <DialogFooter><Button variant="outline">Cancel</Button><Button>Add domain</Button></DialogFooter>
    </DialogContent>
  </Dialog>
);
