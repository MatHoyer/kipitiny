import { CircleAlert, CircleCheck, X } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { friendlyError } from "@/lib/errors";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";
import { ApiError, api } from "../../api";

/** Files sent at once. */
const PARALLEL = 2;

export type Upload = {
  id: number;
  /** "<volume>/<path>" of the file to write. */
  path: string;
  file: File;
  sent: number;
  state: "queued" | "sending" | "done" | "exists" | "failed";
  error?: string;
  overwrite?: boolean;
};

/** A dropped or picked file with its path below the folder it lands in. */
export type Picked = { file: File; rel: string };

let nextId = 0;

/**
 * The upload queue of a service's volumes: files go PARALLEL at a time, an
 * existing file waits for the user to replace or skip it. onDone runs as each
 * file lands (to refresh the listing), onIdle once the queue drains.
 */
export function useUploads(serviceId: string, onDone: () => void, onIdle: (written: number) => void) {
  const [items, setItems] = useState<Upload[]>([]);
  const aborts = useRef(new Map<number, AbortController>());
  const written = useRef(0);
  const callbacks = useRef({ onDone, onIdle });
  callbacks.current = { onDone, onIdle };

  const patch = useCallback((id: number, p: Partial<Upload>) => setItems((all) => all.map((u) => (u.id === id ? { ...u, ...p } : u))), []);

  // Start queued uploads while fewer than PARALLEL are sending.
  useEffect(() => {
    const sending = items.filter((u) => u.state === "sending").length;
    const next = items.filter((u) => u.state === "queued").slice(0, PARALLEL - sending);
    if (sending === 0 && next.length === 0) {
      if (written.current > 0) callbacks.current.onIdle(written.current);
      written.current = 0;
      return;
    }
    for (const u of next) {
      const ctl = new AbortController();
      aborts.current.set(u.id, ctl);
      patch(u.id, { state: "sending", sent: 0, error: undefined });
      api
        .uploadVolumeFile(serviceId, u.path, u.file, { overwrite: u.overwrite, signal: ctl.signal, onProgress: (sent) => patch(u.id, { sent }) })
        .then(() => {
          written.current++;
          patch(u.id, { state: "done", sent: u.file.size });
          callbacks.current.onDone();
        })
        .catch((err) => {
          if (err instanceof DOMException && err.name === "AbortError") return;
          if (err instanceof ApiError && err.status === 409) patch(u.id, { state: "exists" });
          else patch(u.id, { state: "failed", error: friendlyError(err) });
        })
        .finally(() => aborts.current.delete(u.id));
    }
  }, [items, serviceId, patch]);

  const add = (dir: string, picked: Picked[]) =>
    setItems((all) => [
      ...all,
      ...picked.map((p): Upload => ({ id: nextId++, path: `${dir}/${p.rel}`, file: p.file, sent: 0, state: "queued" })),
    ]);
  const replace = (id?: number) =>
    setItems((all) => all.map((u) => (u.state === "exists" && (id === undefined || u.id === id) ? { ...u, state: "queued", overwrite: true } : u)));
  const retry = (id: number) => patch(id, { state: "queued" });
  const remove = (id: number) => {
    aborts.current.get(id)?.abort();
    setItems((all) => all.filter((u) => u.id !== id));
  };
  const clear = () => setItems((all) => all.filter((u) => u.state === "queued" || u.state === "sending"));
  return { items, add, replace, retry, remove, clear };
}

/** The queue under the listing: progress, conflicts to resolve, failures. */
export function UploadPanel({ uploads }: { uploads: ReturnType<typeof useUploads> }) {
  const { items } = uploads;
  if (items.length === 0) return null;
  const active = items.filter((u) => u.state === "queued" || u.state === "sending");
  const exists = items.filter((u) => u.state === "exists");
  const total = active.reduce((n, u) => n + u.file.size, 0);
  const sent = active.reduce((n, u) => n + u.sent, 0);
  return (
    <div className="rounded-lg border bg-card text-sm">
      <div className="flex flex-wrap items-center gap-2 border-b px-3 py-2">
        <span className="font-medium">
          {active.length > 0
            ? `Uploading ${active.length} file${active.length === 1 ? "" : "s"} · ${formatBytes(sent)} of ${formatBytes(total)}`
            : "Uploads"}
        </span>
        <span className="ml-auto flex gap-1.5">
          {exists.length > 1 && (
            <Button size="xs" variant="outline" onClick={() => uploads.replace()}>
              Replace all {exists.length}
            </Button>
          )}
          {active.length < items.length && (
            <Button size="xs" variant="ghost" onClick={uploads.clear}>
              Clear finished
            </Button>
          )}
        </span>
      </div>
      <ul className="max-h-56 divide-y overflow-y-auto">
        {items.map((u) => (
          <li key={u.id} className="flex items-center gap-2 px-3 py-1.5">
            <UploadIcon state={u.state} />
            <span className="min-w-0 flex-1">
              <span className="block truncate font-mono text-xs" title={u.path}>
                {u.path}
              </span>
              {u.state === "sending" && (
                <span className="mt-1 block h-1 overflow-hidden rounded-full bg-muted">
                  <span className="block h-full bg-primary transition-[width]" style={{ width: `${u.file.size ? (100 * u.sent) / u.file.size : 100}%` }} />
                </span>
              )}
              {u.state === "exists" && <span className="block text-xs text-muted-foreground">Already exists.</span>}
              {u.state === "failed" && <span className="block text-xs text-destructive">{u.error}</span>}
            </span>
            <span className="shrink-0 text-xs text-muted-foreground tabular-nums">{formatBytes(u.file.size)}</span>
            {u.state === "exists" && (
              <Button size="xs" variant="outline" onClick={() => uploads.replace(u.id)}>
                Replace
              </Button>
            )}
            {u.state === "failed" && (
              <Button size="xs" variant="outline" onClick={() => uploads.retry(u.id)}>
                Retry
              </Button>
            )}
            <Button
              size="icon-xs"
              variant="ghost"
              title={u.state === "sending" || u.state === "queued" ? "Cancel" : u.state === "exists" ? "Skip" : "Dismiss"}
              onClick={() => uploads.remove(u.id)}
            >
              <X />
            </Button>
          </li>
        ))}
      </ul>
    </div>
  );
}

function UploadIcon({ state }: { state: Upload["state"] }) {
  const cls = "size-4 shrink-0";
  switch (state) {
    case "done":
      return <CircleCheck className={cn(cls, "text-emerald-600 dark:text-emerald-400")} />;
    case "failed":
      return <CircleAlert className={cn(cls, "text-destructive")} />;
    case "exists":
      return <CircleAlert className={cn(cls, "text-amber-600 dark:text-amber-400")} />;
    case "sending":
      return <Spinner className={cls} />;
  }
  return <span className={cn(cls, "rounded-full border-2 border-muted")} />;
}

/** The files of a drop, folders walked recursively with their relative paths. */
export async function droppedFiles(dt: DataTransfer): Promise<Picked[]> {
  const entries = [...dt.items].map((i) => i.webkitGetAsEntry?.()).filter((e): e is FileSystemEntry => !!e);
  if (entries.length === 0) return [...dt.files].map((file) => ({ file, rel: file.name }));
  const out: Picked[] = [];
  const walk = async (entry: FileSystemEntry, prefix: string): Promise<void> => {
    if (entry.isFile) {
      const file = await new Promise<File>((res, rej) => (entry as FileSystemFileEntry).file(res, rej));
      out.push({ file, rel: prefix + entry.name });
    } else if (entry.isDirectory) {
      const reader = (entry as FileSystemDirectoryEntry).createReader();
      // readEntries returns batches until an empty one.
      for (;;) {
        const batch = await new Promise<FileSystemEntry[]>((res, rej) => reader.readEntries(res, rej));
        if (batch.length === 0) break;
        for (const e of batch) await walk(e, `${prefix}${entry.name}/`);
      }
    }
  };
  for (const e of entries) await walk(e, "");
  return out;
}

/** Files from an <input type="file">, with a picked folder's relative paths. */
export const pickedFiles = (files: FileList): Picked[] => [...files].map((file) => ({ file, rel: file.webkitRelativePath || file.name }));
