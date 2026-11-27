import { Container, Copy, Download, Ellipsis, Eye, File, FileSymlink, Folder, FolderInput, HardDrive, Pencil, TextCursorInput, Trash2 } from "lucide-react";
import { Button } from "@kipit-labs/kipitidy/button";
import { Checkbox } from "@kipit-labs/kipitidy/checkbox";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@kipit-labs/kipitidy/dropdown-menu";
import { formatBytes, formatDateTime, timeAgo } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { VolumeEntry } from "../../api";
import type { FilesDialog } from "./files-dialogs";

/** A row of the files table: the entry, its details and its actions menu. */
export function EntryRow({
  entry: e,
  atRoot,
  writable,
  selected,
  downloadUrl,
  onOpen,
  onToggle,
  onEdit,
  onDialog,
}: {
  entry: VolumeEntry;
  atRoot: boolean;
  writable: boolean;
  selected: boolean;
  downloadUrl: string;
  onOpen: () => void;
  onToggle: () => void;
  onEdit: () => void;
  onDialog: (d: FilesDialog) => void;
}) {
  return (
    <tr
      aria-selected={selected}
      onClick={onOpen}
      className="group cursor-pointer hover:bg-muted/60 aria-selected:bg-muted"
    >
      <td className="border-b py-3 pl-4" onClick={(ev) => ev.stopPropagation()}>
        {!atRoot && <Checkbox aria-label={`Select ${e.name}`} checked={selected} onCheckedChange={onToggle} />}
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
              <DropdownMenuItem onSelect={onEdit}>
                {writable ? <Pencil /> : <Eye />} {writable ? "Edit" : "View"}
              </DropdownMenuItem>
            )}
            {e.type !== "container" && (
              <DropdownMenuItem asChild>
                <a href={downloadUrl} download>
                  <Download /> Download{e.type !== "file" && " (.tar.gz)"}
                </a>
              </DropdownMenuItem>
            )}
            {e.type === "container" && (
              <DropdownMenuItem onSelect={onOpen}>
                <Folder /> Open
              </DropdownMenuItem>
            )}
            {writable && (
              <>
                <DropdownMenuItem onSelect={() => onDialog({ kind: "rename", entry: e })}>
                  <TextCursorInput /> Rename
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => onDialog({ kind: "move", names: [e.name] })}>
                  <FolderInput /> Move…
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => onDialog({ kind: "copy", names: [e.name] })}>
                  <Copy /> Copy…
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem variant="destructive" onSelect={() => onDialog({ kind: "delete", names: [e.name] })}>
                  <Trash2 /> Delete…
                </DropdownMenuItem>
              </>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      </td>
    </tr>
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
