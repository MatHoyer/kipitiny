import { Card, CardContent, CardHeader, Skeleton } from 'homelab-ui';

export const Lines = () => (
  <div className="grid w-72 gap-2"><Skeleton className="h-4 w-3/4" /><Skeleton className="h-4 w-full" /><Skeleton className="h-4 w-1/2" /></div>
);
export const CardLoading = () => (
  <Card className="w-80">
    <CardHeader><Skeleton className="h-5 w-40" /></CardHeader>
    <CardContent className="grid gap-2"><Skeleton className="h-4 w-full" /><Skeleton className="h-4 w-2/3" /></CardContent>
  </Card>
);
