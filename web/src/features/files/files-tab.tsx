import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, Copy, Download, File, FolderInput, FolderPlus, FolderUp, Lock, RefreshCw, Trash2, TriangleAlert, Upload } from "lucide-react";
import { useRef, useState, type DragEvent, type ReactNode } from "react";
import { useSearchParams } from "react-router";
import { toast } from "sonner";
import { ErrorText } from "@/components/common";
import { Loading } from "@kipit-labs/kipitidy/loading";
import { Button } from "@kipit-labs/kipitidy/button";
import { Checkbox } from "@kipit-labs/kipitidy/checkbox";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@kipit-labs/kipitidy/select";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@kipit-labs/kipitidy/dropdown-menu";
import { cn } from "@/lib/utils";
import { api, isDatabase, type Service, type VolumeEntry } from "../../api";
import { EntryRow } from "./entry-row";
import { FileDialog } from "./file-dialog";
import { FilesDialogs, type FilesDialog } from "./files-dialogs";
import { CONTAINER, inContainer, join } from "./paths";
import { droppedFiles, pickedFiles, UploadPanel, useUploads } from "./uploads";

/**
 * Browse and manage the files in a service's volumes. Databases' are
 * read-only. The folder is kept in the URL (?path=), next to the tab.
 */
export function FilesTab({ svc, onBackups }: { svc: Service; onBackups: () => void }) {
  const qc = useQueryClient();
  const [params, setParams] = useSearchParams();
  const dir = (params.get("path") ?? "").replace(/^\/+|\/+$/g, "");
  const go = (p: string) =>
    setParams(
      (q) => {
        if (p) q.set("path", p);
        else q.delete("path");
        return q;
      },
      { replace: false },
    );
  // A selection belongs to the folder it was made in.
  const [selection, setSelection] = useState<{ dir: string; names: Set<string> }>({ dir, names: new Set() });
  const selected = selection.dir === dir ? selection.names : new Set<string>();
  const setSelected = (next: Set<string> | ((s: Set<string>) => Set<string>)) =>
    setSelection((cur) => ({ dir, names: typeof next === "function" ? next(cur.dir === dir ? cur.names : new Set()) : next }));
  const [dialog, setDialog] = useState<FilesDialog | null>(null);
  const [dragging, setDragging] = useState(false);
  const [openFile, setOpenFile] = useState<string | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const folderInput = useRef<HTMLInputElement>(null);

  const listing = useQuery({ queryKey: ["files", svc.id, dir], queryFn: () => api.volumeFiles(svc.id, dir) });
  const refresh = () => qc.invalidateQueries({ queryKey: ["files", svc.id] });
  const running = svc.containers.some((c) => !c.retired && c.state === "running");
  const restart = useMutation({
    meta: { error: "Couldn't restart the service" },
    mutationFn: () => api.serviceAction(svc.id, "restart"),
    onSuccess: (s) => qc.setQueryData(["service", svc.id], s),
  });
  /** After a change: refresh, and offer a restart, which most apps need to load new files. */
  const changed = (message: string) => {
    refresh();
    setSelected(new Set());
    if (running && svc.kind === "app")
      toast.success(message, {
        description: "Most apps load new files on restart.",
        action: { label: "Restart", onClick: () => restart.mutate() },
      });
    else toast.success(message);
  };
  const uploads = useUploads(svc.id, refresh, (n) => changed(n === 1 ? "File uploaded" : `${n} files uploaded`));

  const mkdir = useMutation({
    meta: { error: "Couldn't create the folder" },
    mutationFn: (name: string) => api.makeVolumeDir(svc.id, join(dir, name)),
    onSuccess: () => refresh(),
  });
  const rename = useMutation({
    meta: { error: "Couldn't rename it" },
    mutationFn: ({ from, to }: { from: string; to: string }) => api.moveVolumePath(svc.id, join(dir, from), join(dir, to)),
    onSuccess: () => changed("Renamed"),
  });
  const transfer = useMutation({
    meta: { error: "Couldn't finish" },
    mutationFn: async ({ kind, names, to }: { kind: "move" | "copy"; names: string[]; to: string }) => {
      const op = kind === "move" ? api.moveVolumePath : api.copyVolumePath;
      for (const n of names) await op(svc.id, join(dir, n), join(to, n));
    },
    onSuccess: (_, v) => changed(`${v.kind === "move" ? "Moved" : "Copied"} to ${v.to}`),
    onSettled: () => refresh(),
  });
  const remove = useMutation({
    meta: { error: "Couldn't delete" },
    mutationFn: (names: string[]) => api.deleteVolumePaths(svc.id, names.map((n) => join(dir, n))),
    onSuccess: (_, names) => changed(names.length === 1 ? `Deleted ${names[0]}` : `Deleted ${names.length} items`),
  });

  const data = listing.data;
  const readOnly = data?.readOnly ?? isDatabase(svc.kind);
  const atRoot = dir === "";
  // Nothing to write into when the folder can't be listed (a stopped container, a missing folder).
  const writable = !listing.error && !readOnly && !atRoot;
  const entries = data?.entries ?? [];
  const picked = entries.filter((e) => selected.has(e.name));
  const allPicked = entries.length > 0 && picked.length === entries.length;
  const toggle = (name: string) =>
    setSelected((s) => {
      const next = new Set(s);
      if (!next.delete(name)) next.add(name);
      return next;
    });
  const open = (e: VolumeEntry) => {
    if (e.type === "volume" || e.type === "container" || e.type === "dir" || e.type === "link") go(join(dir, e.name));
    else if (e.type === "file") setOpenFile(join(dir, e.name));
    else toggle(e.name);
  };
  const downloadUrl = (names: string[]) => {
    const one = names.length === 1 ? entries.find((e) => e.name === names[0]) : undefined;
    if (atRoot || one?.type === "file") return api.volumeDownloadUrl(svc.id, join(dir, names[0]));
    return api.volumeDownloadUrl(svc.id, dir, names);
  };

  const onDrop = async (e: DragEvent) => {
    e.preventDefault();
    setDragging(false);
    if (!writable) return;
    uploads.add(dir, await droppedFiles(e.dataTransfer));
  };
  const onDragOver = (e: DragEvent) => {
    if (!writable || !e.dataTransfer.types.includes("Files")) return;
    e.preventDefault();
    setDragging(true);
  };

  const crumbs = dir ? dir.split("/") : [];
  const replicas = svc.containers.filter((c) => !c.retired && c.state === "running").sort((a, b) => a.replica - b.replica);
  const ref = inContainer(dir) ? crumbs[0] : null;
  const replica = ref === CONTAINER ? replicas[0] : replicas.find((c) => ref && c.id.startsWith(ref.slice(1)));
  const pickReplica = (id: string) => go([`@${id.slice(0, 12)}`, ...crumbs.slice(1)].join("/"));

  return (
    <div className="space-y-3">
      {readOnly && (
        <p className="flex flex-wrap items-center gap-x-1.5 rounded-lg border bg-muted/40 px-3 py-2 text-sm text-muted-foreground">
          <Lock className="size-3.5 shrink-0" />
          Read-only. The database writes these files while it runs, so a download isn't a consistent copy:
          <button type="button" className="font-medium text-foreground underline-offset-4 hover:underline" onClick={onBackups}>
            use Backups
          </button>
          for that.
        </p>
      )}

      {ref && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm">
          <TriangleAlert className="size-4 shrink-0 text-amber-600 dark:text-amber-400" />
          <p className="min-w-[min(100%,16rem)] flex-1">
            The container's own files.{" "}
            {readOnly
              ? "Read-only for a database."
              : "Changes reach this replica only and are lost on the next deploy or recreate: keep files that matter in a volume."}
          </p>
          {replicas.length > 1 && (
            <Select value={replica?.id ?? ""} onValueChange={pickReplica}>
              <SelectTrigger size="sm" aria-label="Replica" className="bg-background">
                <SelectValue placeholder="Replica" />
              </SelectTrigger>
              <SelectContent>
                {replicas.map((c) => (
                  <SelectItem key={c.id} value={c.id}>
                    Replica {c.replica} <span className="font-mono text-xs text-muted-foreground">{c.id.slice(0, 12)}</span>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        </div>
      )}

      <div className="flex flex-wrap items-center gap-1.5">
        <nav aria-label="Folder" className="flex min-w-0 basis-full flex-wrap items-center gap-0.5 font-mono text-sm sm:flex-1 sm:basis-auto">
          <Crumb onClick={() => go("")} current={atRoot}>
            Files
          </Crumb>
          {crumbs.map((c, i) => (
            <span key={i} className="flex items-center gap-0.5">
              <ChevronRight className="size-3.5 text-muted-foreground" />
              <Crumb onClick={() => go(crumbs.slice(0, i + 1).join("/"))} current={i === crumbs.length - 1}>
                {i === 0 && inContainer(c) ? (replica && replicas.length > 1 ? `container (replica ${replica.replica})` : "container") : c}
              </Crumb>
            </span>
          ))}
        </nav>
        {picked.length > 0 ? (
          <>
            <span className="px-1 text-sm text-muted-foreground">{picked.length} selected</span>
            <Button size="sm" variant="outline" asChild>
              <a href={downloadUrl(picked.map((e) => e.name))} download>
                <Download /> Download
              </a>
            </Button>
            {writable && (
              <>
                <Button size="sm" variant="outline" onClick={() => setDialog({ kind: "move", names: picked.map((e) => e.name) })}>
                  <FolderInput /> Move
                </Button>
                <Button size="sm" variant="outline" onClick={() => setDialog({ kind: "copy", names: picked.map((e) => e.name) })}>
                  <Copy /> Copy
                </Button>
                <Button size="sm" variant="outline" className="text-destructive" onClick={() => setDialog({ kind: "delete", names: picked.map((e) => e.name) })}>
                  <Trash2 /> Delete
                </Button>
              </>
            )}
          </>
        ) : (
          writable && (
            <>
              <Button size="sm" variant="outline" onClick={() => setDialog({ kind: "mkdir" })}>
                <FolderPlus /> New folder
              </Button>
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button size="sm">
                    <Upload /> Upload
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuItem onSelect={() => fileInput.current?.click()}>
                    <File /> Files
                  </DropdownMenuItem>
                  <DropdownMenuItem onSelect={() => folderInput.current?.click()}>
                    <FolderUp /> Folder
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </>
          )
        )}
        <Button size="icon-sm" variant="ghost" title="Refresh" onClick={refresh}>
          <RefreshCw className={cn(listing.isFetching && "animate-spin")} />
        </Button>
      </div>

      <input
        ref={fileInput}
        type="file"
        multiple
        hidden
        onChange={(e) => {
          if (e.target.files) uploads.add(dir, pickedFiles(e.target.files));
          e.target.value = "";
        }}
      />
      <input
        ref={folderInput}
        type="file"
        hidden
        // @ts-expect-error: not in React's types, supported by every current browser.
        webkitdirectory=""
        onChange={(e) => {
          if (e.target.files) uploads.add(dir, pickedFiles(e.target.files));
          e.target.value = "";
        }}
      />

      <div
        onDragOver={onDragOver}
        onDragLeave={(e) => !e.currentTarget.contains(e.relatedTarget as Node) && setDragging(false)}
        onDrop={onDrop}
        className={cn("relative max-h-[65vh] min-h-48 overflow-auto rounded-lg border bg-card", dragging && "border-primary ring-[3px] ring-primary/30")}
      >
        {listing.isPending ? (
          <Loading />
        ) : listing.error ? (
          // Centered like an empty folder; the path above leads back up.
          <div className="flex min-h-48 items-center justify-center px-6 py-10 text-center">
            <ErrorText error={listing.error} />
          </div>
        ) : (
          <table className="w-full border-separate border-spacing-0 text-sm">
            <thead className="sticky top-0 z-10 bg-card text-left text-xs text-muted-foreground">
              <tr>
                <th scope="col" className="w-10 border-b py-2.5 pl-4">
                  {!atRoot && entries.length > 0 && (
                    <Checkbox
                      aria-label="Select all"
                      checked={allPicked ? true : picked.length > 0 ? "indeterminate" : false}
                      onCheckedChange={() => setSelected(allPicked ? new Set() : new Set(entries.map((e) => e.name)))}
                    />
                  )}
                </th>
                <th scope="col" className="w-full border-b px-3 py-2.5 font-normal">
                  Name
                </th>
                {atRoot ? (
                  <th scope="col" className="border-b px-3 py-2.5 font-normal whitespace-nowrap">
                    Mounted at
                  </th>
                ) : (
                  <>
                    <th scope="col" className="border-b px-3 py-2.5 text-right font-normal">
                      Size
                    </th>
                    <th scope="col" className="hidden border-b px-3 py-2.5 font-normal sm:table-cell">
                      Modified
                    </th>
                    <th scope="col" className="hidden border-b px-3 py-2.5 font-normal md:table-cell">
                      Permissions
                    </th>
                  </>
                )}
                <th scope="col" className="w-12 border-b" aria-label="Actions" />
              </tr>
            </thead>
            <tbody>
              {entries.map((e) => (
                <EntryRow
                  key={e.name}
                  entry={e}
                  atRoot={atRoot}
                  writable={writable}
                  selected={selected.has(e.name)}
                  downloadUrl={downloadUrl([e.name])}
                  onOpen={() => open(e)}
                  onToggle={() => toggle(e.name)}
                  onEdit={() => setOpenFile(join(dir, e.name))}
                  onDialog={setDialog}
                />
              ))}
            </tbody>
          </table>
        )}
        {data && entries.length === 0 && (
          <p className="px-3 py-10 text-center text-sm text-muted-foreground">
            {writable ? "Empty folder. Drop files here or use Upload." : "Empty folder."}
          </p>
        )}
        {dragging && (
          <div className="pointer-events-none absolute inset-0 flex items-center justify-center bg-background/70 text-sm font-medium">
            Drop to upload into {dir}
          </div>
        )}
      </div>
      {atRoot && data && svc.kind === "app" && svc.volumes.length === 0 && (
        <p className="text-sm text-muted-foreground">
          This app has no volumes: its files live in the container and are lost on every deploy. Add a volume in Settings › Volumes to keep
          them.
        </p>
      )}
      {data?.truncated && <p className="text-xs text-muted-foreground">Showing the first {entries.length} entries of this folder.</p>}

      <UploadPanel uploads={uploads} />

      <FileDialog serviceId={svc.id} path={openFile} onClose={() => setOpenFile(null)} onSaved={() => changed("Saved")} />

      <FilesDialogs
        dialog={dialog}
        dir={dir}
        onClose={() => setDialog(null)}
        onMkdir={(name) => mkdir.mutateAsync(name)}
        onRename={(from, to) => rename.mutateAsync({ from, to })}
        onTransfer={(v) => transfer.mutateAsync(v)}
        onDelete={(names) => remove.mutate(names)}
      />
    </div>
  );
}

function Crumb({ onClick, current, children }: { onClick: () => void; current: boolean; children: ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-current={current ? "location" : undefined}
      className={cn("rounded px-1.5 py-0.5 hover:bg-muted", current ? "font-medium text-foreground" : "text-muted-foreground")}
    >
      {children}
    </button>
  );
}
