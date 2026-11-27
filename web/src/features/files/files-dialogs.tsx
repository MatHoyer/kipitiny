import { TriangleAlert } from "lucide-react";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogTitle,
} from "@kipit-labs/kipitidy/alert-dialog";
import { Button } from "@kipit-labs/kipitidy/button";
import type { VolumeEntry } from "../../api";
import { PathDialog } from "./path-dialog";
import { inContainer, nameProblem, pathProblem } from "./paths";

export type FilesDialog = { kind: "mkdir" } | { kind: "rename"; entry: VolumeEntry } | { kind: "move" | "copy" | "delete"; names: string[] };

/** The files tab's dialogs: new folder, rename, move or copy, delete. */
export function FilesDialogs({
  dialog,
  dir,
  onClose,
  onMkdir,
  onRename,
  onTransfer,
  onDelete,
}: {
  dialog: FilesDialog | null;
  dir: string;
  onClose: () => void;
  onMkdir: (name: string) => Promise<unknown>;
  onRename: (from: string, to: string) => Promise<unknown>;
  onTransfer: (v: { kind: "move" | "copy"; names: string[]; to: string }) => Promise<unknown>;
  onDelete: (names: string[]) => void;
}) {
  return (
    <>
    <PathDialog
      open={dialog?.kind === "mkdir"}
      onOpenChange={(o) => !o && onClose()}
      title="New folder"
      description={<>In <span className="font-mono">{dir}</span></>}
      label="Name"
      initial=""
      submitLabel="Create"
      validate={nameProblem}
      onSubmit={onMkdir}
    />
    <PathDialog
      open={dialog?.kind === "rename"}
      onOpenChange={(o) => !o && onClose()}
      title="Rename"
      label="Name"
      initial={dialog?.kind === "rename" ? dialog.entry.name : ""}
      submitLabel="Rename"
      validate={nameProblem}
      onSubmit={(to) => (dialog?.kind === "rename" && to !== dialog.entry.name ? onRename(dialog.entry.name, to) : Promise.resolve())}
    />
    <PathDialog
      open={dialog?.kind === "move" || dialog?.kind === "copy"}
      onOpenChange={(o) => !o && onClose()}
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
          ? onTransfer({ kind: dialog.kind, names: dialog.names, to: to.replace(/\/+$/, "") })
          : Promise.resolve()
      }
    />
    <AlertDialog open={dialog?.kind === "delete"} onOpenChange={(o) => !o && onClose()}>
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
              if (dialog?.kind === "delete") onDelete(dialog.names);
              onClose();
            }}
          >
            Delete
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
    </>
  );
}
