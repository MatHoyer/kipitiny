import { Button, Card, CardAction, CardContent, CardDescription, CardFooter, CardHeader, CardTitle, FloatingInput } from 'homelab-ui';

export const Default = () => (
  <Card className="w-96">
    <CardHeader>
      <CardTitle>Billing API</CardTitle>
      <CardDescription>Deployed 2 hours ago from main.</CardDescription>
      <CardAction><Button size="sm" variant="outline">Open</Button></CardAction>
    </CardHeader>
    <CardContent className="text-muted-foreground">3 services running, 1.2 GB of memory in use.</CardContent>
  </Card>
);
export const SignIn = () => (
  <Card className="w-96">
    <CardHeader><CardTitle>Sign in</CardTitle></CardHeader>
    <CardContent className="space-y-4">
      <FloatingInput label="Username" />
      <FloatingInput label="Password" type="password" />
      <Button size="lg" className="mt-2 h-11 w-full rounded-xl">Sign in</Button>
    </CardContent>
  </Card>
);
export const WithFooter = () => (
  <Card size="sm" className="w-80">
    <CardHeader><CardTitle>Delete old backups</CardTitle></CardHeader>
    <CardContent className="text-muted-foreground">Frees about 4 GB.</CardContent>
    <CardFooter className="justify-end gap-2"><Button size="sm" variant="ghost">Later</Button><Button size="sm">Run now</Button></CardFooter>
  </Card>
);
