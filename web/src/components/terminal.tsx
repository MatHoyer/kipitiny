import { FitAddon } from "@xterm/addon-fit";
import { Terminal as XTerm } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { useEffect, useRef } from "react";
import { cn } from "@/lib/utils";

/** What the server sends last, as a text frame; the output comes as binary frames. */
type TerminalEnd = { type: "exit" | "error"; code: number; error?: string };

/**
 * An interactive shell over a websocket. Mounting it opens the session,
 * unmounting it hangs up. `path` is an API path; the terminal size is added
 * to its query.
 */
export function TerminalView({ path, onClose, className }: { path: string; onClose?: () => void; className?: string }) {
  const host = useRef<HTMLDivElement>(null);
  const closed = useRef(onClose);
  closed.current = onClose;

  useEffect(() => {
    const el = host.current!;
    const term = new XTerm({
      cursorBlink: true,
      fontSize: 13,
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
      scrollback: 5000,
      theme: { background: "#0a0a0a" },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(el);
    fit.fit();

    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const sep = path.includes("?") ? "&" : "?";
    const ws = new WebSocket(`${proto}//${location.host}${path}${sep}cols=${term.cols}&rows=${term.rows}`);
    ws.binaryType = "arraybuffer";
    let ended = false;
    const send = (m: object) => ws.readyState === WebSocket.OPEN && ws.send(JSON.stringify(m));

    ws.onopen = () => term.focus();
    ws.onmessage = (e) => {
      if (typeof e.data !== "string") {
        term.write(new Uint8Array(e.data as ArrayBuffer));
        return;
      }
      const end = JSON.parse(e.data) as TerminalEnd;
      ended = true;
      term.write(
        end.type === "error"
          ? `\r\n\x1b[31m${end.error}\x1b[0m\r\n`
          : `\r\n\x1b[90m[exited with code ${end.code}]\x1b[0m\r\n`,
      );
    };
    ws.onclose = () => {
      if (!ended) term.write("\r\n\x1b[90m[connection closed]\x1b[0m\r\n");
      closed.current?.();
    };
    const input = term.onData((data) => send({ type: "input", data }));
    const resize = term.onResize(({ cols, rows }) => send({ type: "resize", cols, rows }));
    const observer = new ResizeObserver(() => fit.fit());
    observer.observe(el);

    return () => {
      observer.disconnect();
      input.dispose();
      resize.dispose();
      ws.onclose = null;
      ws.close();
      term.dispose();
    };
  }, [path]);

  return (
    <div
      className={cn(
        "overflow-hidden rounded-lg bg-neutral-950 p-3 dark:ring-1 dark:ring-foreground/10",
        className,
      )}
    >
      <div ref={host} className="size-full" />
    </div>
  );
}
