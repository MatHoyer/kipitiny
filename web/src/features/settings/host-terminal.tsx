import { SquareTerminal } from "lucide-react";
import { lazy, Suspense, useState } from "react";
import { Loading } from "@kipit-labs/kipitidy/loading";
import { Button } from "@kipit-labs/kipitidy/button";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle, SheetTrigger } from "@kipit-labs/kipitidy/sheet";
import { api, type Server } from "@/api";

// xterm.js is big: load it with the first terminal.
const TerminalView = lazy(() => import("@/features/services/terminal").then((m) => ({ default: m.TerminalView })));

/** A root shell on the host, through a privileged helper container, in a panel over the page. */
export function HostTerminal({ server }: { server: Server }) {
  // Each (re)connect mounts a new session; an ended one stays on screen.
  // Closing the panel unmounts the terminal, which ends the session.
  const [session, setSession] = useState(0);
  const [live, setLive] = useState(false);
  const connect = () => {
    setSession((n) => n + 1);
    setLive(true);
  };

  return (
    <Sheet onOpenChange={(open) => open && connect()}>
      <SheetTrigger asChild>
        <Button size="sm" variant="outline" disabled={!server.docker}>
          <SquareTerminal data-icon="inline-start" />
          Terminal
        </Button>
      </SheetTrigger>
      <SheetContent side="bottom" className="h-[75vh] gap-3 p-4 sm:p-6">
        <SheetHeader className="flex-row flex-wrap items-center gap-x-4 gap-y-1 p-0 pr-10">
          <div className="min-w-0 flex-1 space-y-1">
            <SheetTitle>Terminal on {server.name}</SheetTitle>
            <SheetDescription>Root shell on the host. Sessions are recorded in the audit log.</SheetDescription>
          </div>
          {!live && (
            <Button size="sm" onClick={connect}>
              Reconnect
            </Button>
          )}
        </SheetHeader>
        <Suspense fallback={<Loading />}>
          <TerminalView key={session} path={api.serverTerminalUrl(server.id)} onClose={() => setLive(false)} className="min-h-0 flex-1" />
        </Suspense>
      </SheetContent>
    </Sheet>
  );
}
