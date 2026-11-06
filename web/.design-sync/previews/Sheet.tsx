import { Button, FloatingInput, Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from 'homelab-ui';

export const Open = () => (
  <Sheet defaultOpen>
    <SheetContent onOpenAutoFocus={(e) => e.preventDefault()}>
      <SheetHeader>
        <SheetTitle>Edit service</SheetTitle>
        <SheetDescription>Changes apply on the next deploy.</SheetDescription>
      </SheetHeader>
      <div className="grid gap-4 px-4">
        <FloatingInput label="Name" defaultValue="api" />
        <FloatingInput label="Image" defaultValue="ghcr.io/acme/api:1.4.2" />
      </div>
      <SheetFooter><Button>Save changes</Button></SheetFooter>
    </SheetContent>
  </Sheet>
);
