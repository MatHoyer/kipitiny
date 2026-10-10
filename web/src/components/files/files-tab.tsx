import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ChevronRight,
  Container,
  Copy,
  Download,
  Ellipsis,
  Eye,
  File,
  FileSymlink,
  Folder,
  FolderInput,
  FolderPlus,
  FolderUp,
  HardDrive,
  Lock,
  Pencil,
  RefreshCw,
  TextCursorInput,
  Trash2,
  TriangleAlert,
  Upload,
} from "lucide-react";
import { useRef, useState, type DragEvent, type ReactNode } from "react";
import { useSearchParams } from "react-router";
import { toast } from "sonner";
import { ErrorText, Loading } from "@/components/common";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { formatBytes, formatDateTime, timeAgo } from "@/lib/format";
import { cn } from "@/lib/utils";
import { api, isDatabase, type Service, type VolumeEntry } from "../../api";
import { FileDialog } from "./file-dialog";
import { PathDialog } from "./path-dialog";
import { droppedFiles, pickedFiles, UploadPanel, useUploads } from "./uploads";

const join = (dir: string, name: string) => (dir ? `${dir}/${name}` : name);
/** "@container" is the first running replica's filesystem, "@<container ID>" a given one's. */
const CONTAINER = "@container";
const inContainer = (p: string) => p.startsWith("@");

/** A name for an entry in a folder: no slashes, not . or .. */
const nameProblem = (name: string) =>
  name.includes("/") ? "A name can't contain /." : name === "." || name === ".." ? "Pick another name." : null;

/** A destination "<volume>/<path>": relative, without . or .. segments. */
const pathProblem = (p: string) =>
  p.startsWith("/") ? "Start with the volume's name, e.g. data/mods." : p.split("/").some((s) => s === "." || s === "..") ? "Use a path without . or .." : null;

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
  const [dialog, setDialog] = useState<null | { kind: "mkdir" } | { kind: "rename"; entry: VolumeEntry } | { kind: "move" | "copy" | "delete"; names: string[] }>(null);
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
                // The whole row opens the entry; the name is its keyboard target (Enter clicks it, the click bubbles here).
                <tr
                  key={e.name}
                  aria-selected={selected.has(e.name)}
                  onClick={() => open(e)}
                  className="group cursor-pointer hover:bg-muted/60 aria-selected:bg-muted"
                >
                  <td className="border-b py-3 pl-4" onClick={(ev) => ev.stopPropagation()}>
                    {!atRoot && <Checkbox aria-label={`Select ${e.name}`} checked={selected.has(e.name)} onCheckedChange={() => toggle(e.name)} />}
                  </td>
                  <td className="max-w-0 border-b px-3 py-3">
                    <button
                      type="button"
                      className="flex max-w-full items-center gap-3 text-left"
                      title={e.target ? `${e.name} → ${e.target}` : e.name}
                    >
                      <EntryIcon type={e.type} />
                      {e.type === "container" ? (
                        <span className="truncate">Container filesystem</span>
                      ) : (
                        <span className="truncate font-mono text-[13px]">{e.name}</span>
                      )}
                      {e.target && <span className="truncate font-mono text-[13px] text-muted-foreground">→ {e.target}</span>}
                    </button>
                  </td>
                  {atRoot ? (
                    <td className="border-b px-3 py-3 font-mono text-[13px] text-muted-foreground">{e.mountPath}</td>
                  ) : (
                    <>
                      <td className="border-b px-3 py-3 text-right whitespace-nowrap text-muted-foreground tabular-nums">
                        {e.type === "file" ? formatBytes(e.size) : ""}
                      </td>
                      <td
                        className="hidden border-b px-3 py-3 whitespace-nowrap text-muted-foreground sm:table-cell"
                        title={e.modified && formatDateTime(e.modified)}
                      >
                        {e.modified && timeAgo(e.modified)}
                      </td>
                      <td className="hidden border-b px-3 py-3 font-mono text-xs whitespace-nowrap text-muted-foreground md:table-cell">
                        {e.mode} · {e.uid}:{e.gid}
                      </td>
                    </>
                  )}
                  {/* Menu clicks (its items too: they're portaled, but React bubbles them here) don't open the row. */}
                  <td className="border-b pr-3 text-right" onClick={(ev) => ev.stopPropagation()}>
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button size="icon-sm" variant="ghost" aria-label={`Actions for ${e.name}`}>
                          <Ellipsis />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        {e.type === "file" && (
                          <DropdownMenuItem onSelect={() => setOpenFile(join(dir, e.name))}>
                            {writable ? <Pencil /> : <Eye />} {writable ? "Edit" : "View"}
                          </DropdownMenuItem>
                        )}
                        {e.type !== "container" && (
                          <DropdownMenuItem asChild>
                            <a href={downloadUrl([e.name])} download>
                              <Download /> Download{e.type !== "file" && " (.tar.gz)"}
                            </a>
                          </DropdownMenuItem>
                        )}
                        {e.type === "container" && (
                          <DropdownMenuItem onSelect={() => open(e)}>
                            <Folder /> Open
                          </DropdownMenuItem>
                        )}
                        {writable && (
                          <>
                            <DropdownMenuItem onSelect={() => setDialog({ kind: "rename", entry: e })}>
                              <TextCursorInput /> Rename
                            </DropdownMenuItem>
                            <DropdownMenuItem onSelect={() => setDialog({ kind: "move", names: [e.name] })}>
                              <FolderInput /> Move…
                            </DropdownMenuItem>
                            <DropdownMenuItem onSelect={() => setDialog({ kind: "copy", names: [e.name] })}>
                              <Copy /> Copy…
                            </DropdownMenuItem>
                            <DropdownMenuSeparator />
                            <DropdownMenuItem variant="destructive" onSelect={() => setDialog({ kind: "delete", names: [e.name] })}>
                              <Trash2 /> Delete…
                            </DropdownMenuItem>
                          </>
                        )}
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </td>
                </tr>
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

      <PathDialog
        open={dialog?.kind === "mkdir"}
        onOpenChange={(o) => !o && setDialog(null)}
        title="New folder"
        description={<>In <span className="font-mono">{dir}</span></>}
        label="Name"
        initial=""
        submitLabel="Create"
        validate={nameProblem}
        onSubmit={(name) => mkdir.mutateAsync(name)}
      />
      <PathDialog
        open={dialog?.kind === "rename"}
        onOpenChange={(o) => !o && setDialog(null)}
        title="Rename"
        label="Name"
        initial={dialog?.kind === "rename" ? dialog.entry.name : ""}
        submitLabel="Rename"
        validate={nameProblem}
        onSubmit={(to) => (dialog?.kind === "rename" && to !== dialog.entry.name ? rename.mutateAsync({ from: dialog.entry.name, to }) : Promise.resolve())}
      />
      <PathDialog
        open={dialog?.kind === "move" || dialog?.kind === "copy"}
        onOpenChange={(o) => !o && setDialog(null)}
        title={dialog?.kind === "copy" ? "Copy to" : "Move to"}
        description={
          (dialog?.kind === "move" || dialog?.kind === "copy") && (
            <>
              {dialog.names.length === 1 ? <span className="font-mono">{dialog.names[0]}</span> : `${dialog.names.length} items`} into an existing folder:{" "}
              {inContainer(dir) ? "in this container." : "in any of this service's volumes."}
            </>
          )
        }
        label="Folder (volume/path)"
        initial={dir}
        submitLabel={dialog?.kind === "copy" ? "Copy" : "Move"}
        validate={(to) => pathProblem(to) ?? (dialog?.kind === "move" && to === dir ? "It's already there." : null)}
        onSubmit={(to) =>
          dialog?.kind === "move" || dialog?.kind === "copy"
            ? transfer.mutateAsync({ kind: dialog.kind, names: dialog.names, to: to.replace(/\/+$/, "") })
            : Promise.resolve()
        }
      />
      <AlertDialog open={dialog?.kind === "delete"} onOpenChange={(o) => !o && setDialog(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogMedia className="bg-destructive/10 text-destructive">
              <TriangleAlert />
            </AlertDialogMedia>
            <AlertDialogTitle>
              {dialog?.kind === "delete" && (dialog.names.length === 1 ? `Delete ${dialog.names[0]}?` : `Delete ${dialog.names.length} items?`)}
            </AlertDialogTitle>
            <AlertDialogDescription>Folders go with everything in them. This can't be undone, except from a backup.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <Button
              className="bg-destructive text-white hover:bg-destructive/80"
              onClick={() => {
                if (dialog?.kind === "delete") remove.mutate(dialog.names);
                setDialog(null);
              }}
            >
              Delete
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
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

function EntryIcon({ type }: { type: VolumeEntry["type"] }) {
  const cls = "size-[18px] shrink-0";
  switch (type) {
    case "volume":
      return <HardDrive className={cn(cls, "text-muted-foreground")} />;
    case "container":
      return <Container className={cn(cls, "text-muted-foreground")} />;
    case "dir":
      return <Folder className={cn(cls, "fill-current/15 text-sky-600 dark:text-sky-400")} />;
    case "link":
      return <FileSymlink className={cn(cls, "text-muted-foreground")} />;
  }
  return <File className={cn(cls, "text-muted-foreground")} />;
}
