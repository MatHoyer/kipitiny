import { SquareTerminal } from "lucide-react";
import { lazy, Suspense, useState } from "react";
import { Empty } from "@/components/common";
import { Loading } from "@kipit-labs/kipitidy/loading";
import { Section } from "@kipit-labs/kipitidy/section";
import { Button } from "@kipit-labs/kipitidy/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@kipit-labs/kipitidy/select";
import { api, type Service as ServiceT } from "../../api";

// xterm.js is big: load it with the first terminal.
const TerminalView = lazy(() => import("@/features/services/terminal").then((m) => ({ default: m.TerminalView })));

const shells = [
  { value: "auto", label: "bash, or sh" },
  { value: "sh", label: "sh" },
  { value: "bash", label: "bash" },
];

/** A shell in one of the service's running containers. */
export function ServiceTerminal({ svc }: { svc: ServiceT }) {
  const running = svc.containers.filter((c) => !c.retired && c.state === "running");
  const [picked, setPicked] = useState("");
  const [shell, setShell] = useState("auto");
  // Each connect mounts a new session; an ended one stays on screen.
  const [session, setSession] = useState<{ n: number; path: string } | null>(null);
  const [live, setLive] = useState(false);
  const container = running.some((c) => c.name === picked) ? picked : (running[0]?.name ?? "");

  if (running.length === 0 && !session) return <Empty>No running container.</Empty>;

  return (
    <Section
      plain
      actions={
        <>
          {running.length > 1 && (
            <Select value={container} onValueChange={setPicked} disabled={live}>
              <SelectTrigger size="sm" aria-label="Replica">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {running.map((c) => (
                  <SelectItem key={c.id} value={c.name}>
                    {c.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
          <Select value={shell} onValueChange={setShell} disabled={live}>
            <SelectTrigger size="sm" aria-label="Shell">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {shells.map((s) => (
                <SelectItem key={s.value} value={s.value}>
                  {s.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {live ? (
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                setSession(null);
                setLive(false);
              }}
            >
              Disconnect
            </Button>
          ) : (
            <Button
              size="sm"
              disabled={!container}
              onClick={() => {
                setSession((s) => ({ n: (s?.n ?? 0) + 1, path: api.terminalUrl(svc.id, container, shell) }));
                setLive(true);
              }}
            >
              <SquareTerminal data-icon="inline-start" />
              {session ? "Reconnect" : "Connect"}
            </Button>
          )}
        </>
      }
    >
      {!session ? (
        <p className="text-sm text-muted-foreground">
          Opens a shell in {running.length > 1 ? "the chosen replica" : container}. Sessions are recorded in the audit log.
        </p>
      ) : (
        <Suspense fallback={<Loading />}>
          <TerminalView
            key={session.n}
            path={session.path}
            onClose={() => setLive(false)}
            className="h-[calc(100svh-24rem)] min-h-80"
          />
        </Suspense>
      )}
    </Section>
  );
}
