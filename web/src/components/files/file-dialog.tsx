import { useMutation, useQuery } from "@tanstack/react-query";
import { Download, Lock, TriangleAlert } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { ErrorText, Loading } from "@/components/common";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { formatBytes, formatDateTime } from "@/lib/format";
import { api, ApiError, type VolumeFile } from "../../api";

// The editor is its own chunk, loaded on the first file opened (see data-console.tsx).
type EditorModule = typeof import("./text-editor");
let editorModule: EditorModule | null = null;
let editorLoading: Promise<EditorModule> | null = null;
const loadEditor = () => (editorLoading ??= import("./text-editor").then((m) => (editorModule = m)));

function useTextEditor() {
  const [mod, setMod] = useState(editorModule);
  useEffect(() => {
    if (!mod) loadEditor().then(setMod);
  }, [mod]);
  return mod?.default;
}

const saveKey = /Mac|iPhone|iPad/.test(navigator.userAgent) ? "⌘S" : "Ctrl+S";

/** Opens a text file of a service's volumes to read or edit it. Binary and big files offer a download instead. */
export function FileDialog({
  serviceId,
  path,
  onClose,
  onSaved,
}: {
  serviceId: string;
  /** "<volume>/<path>"; null when closed. */
  path: string | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  // The text being edited and what the file holds, kept as the editor reports
  // them (not through renders), so closing right after a keystroke still asks.
  const texts = useRef({ draft: "", base: "" });
  const [askClose, setAskClose] = useState(false);
  const close = () => (texts.current.draft !== texts.current.base ? setAskClose(true) : onClose());
  return (
    <>
      <Dialog open={path !== null} onOpenChange={(o) => !o && close()}>
        <DialogContent className="flex h-[85vh] flex-col gap-0 p-0 sm:max-w-4xl" onOpenAutoFocus={(e) => e.preventDefault()}>
          {path !== null && <FileView key={path} serviceId={serviceId} path={path} texts={texts.current} onSaved={onSaved} />}
        </DialogContent>
      </Dialog>
      <AlertDialog open={askClose} onOpenChange={setAskClose}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Discard your changes?</AlertDialogTitle>
            <AlertDialogDescription>They aren't saved to the file.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Keep editing</AlertDialogCancel>
            <Button
              className="bg-destructive text-white hover:bg-destructive/80"
              onClick={() => {
                setAskClose(false);
                texts.current = { draft: "", base: "" };
                onClose();
              }}
            >
              Discard
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}

function FileView({
  serviceId,
  path,
  texts,
  onSaved,
}: {
  serviceId: string;
  path: string;
  texts: { draft: string; base: string };
  onSaved: () => void;
}) {
  const Editor = useTextEditor();
  // gcTime 0: a file reopened later is read again, never shown stale.
  const file = useQuery({ queryKey: ["file", serviceId, path], queryFn: () => api.volumeFile(serviceId, path), gcTime: 0, retry: false });
  // What the file holds on the server (as last read or saved), and the text being edited.
  const [base, setBase] = useState<Pick<VolumeFile, "content" | "modified" | "size"> | null>(null);
  const [draft, setDraft] = useState("");
  const [conflict, setConflict] = useState(false);
  useEffect(() => {
    if (!file.data) return;
    setBase(file.data);
    setDraft(file.data.content);
    texts.draft = texts.base = file.data.content;
    setConflict(false);
  }, [file.data, texts]);
  const dirty = !!base && draft !== base.content;
  const edit = (v: string) => {
    texts.draft = v;
    setDraft(v);
  };

  const save = useMutation({
    // Errors show in the dialog, a conflict with its choices.
    meta: { error: false },
    // The text is captured when saving starts: typing during the save stays unsaved.
    onMutate: () => ({ content: draft }),
    mutationFn: (overwrite: boolean) => api.saveVolumeFile(serviceId, path, draft, overwrite ? undefined : base?.modified),
    onSuccess: (e, _, saved) => {
      setBase({ content: saved.content, modified: e.modified, size: e.size });
      texts.base = saved.content;
      setConflict(false);
      onSaved();
    },
    onError: (err) => setConflict(err instanceof ApiError && err.status === 409),
  });
  const readOnly = file.data?.readOnly ?? true;
  const canSave = !readOnly && dirty && !save.isPending;
  const name = path.slice(path.lastIndexOf("/") + 1);

  return (
    <>
      <DialogHeader className="flex-row flex-wrap items-center gap-x-3 gap-y-1 border-b p-4 pr-12">
        <div className="min-w-0 basis-full sm:flex-1 sm:basis-auto">
          <DialogTitle className="truncate font-mono text-sm" title={path}>
            {path}
          </DialogTitle>
          <DialogDescription className="text-xs">
            {base ? (
              <>
                {formatBytes(base.size)}
                {base.modified && <> · modified {formatDateTime(base.modified)}</>}
                {file.data?.mode && (
                  <>
                    {" "}
                    · {file.data.mode} {file.data.uid}:{file.data.gid}
                  </>
                )}
              </>
            ) : file.error ? (
              "Can't be shown here"
            ) : (
              "Loading…"
            )}
          </DialogDescription>
        </div>
        <Button size="sm" variant="outline" asChild>
          <a href={api.volumeDownloadUrl(serviceId, path)} download={name}>
            <Download /> Download
          </a>
        </Button>
        {!readOnly && (
          <Button size="sm" disabled={!canSave} onClick={() => save.mutate(false)}>
            {save.isPending ? "Saving…" : "Save"}
          </Button>
        )}
      </DialogHeader>

      {conflict ? (
        <div role="alert" className="flex flex-wrap items-center gap-2 border-b bg-amber-500/10 px-4 py-2 text-sm">
          <TriangleAlert className="size-4 shrink-0 text-amber-600 dark:text-amber-400" />
          <span className="flex-1">The file changed since you opened it.</span>
          <Button size="xs" variant="outline" onClick={() => file.refetch()}>
            Reload theirs
          </Button>
          <Button size="xs" variant="outline" onClick={() => save.mutate(true)}>
            Overwrite with mine
          </Button>
        </div>
      ) : (
        save.error && (
          <div className="border-b px-4 py-2">
            <ErrorText error={save.error} />
          </div>
        )
      )}

      <div className="min-h-0 flex-1 overflow-hidden">
        {file.isPending || (file.data && !Editor) ? (
          <Loading />
        ) : file.error ? (
          <div className="space-y-3 p-6">
            <ErrorText error={file.error} />
          </div>
        ) : (
          Editor && (
            <Editor
              value={draft}
              onChange={edit}
              onSave={() => canSave && save.mutate(false)}
              readOnly={readOnly}
              label={`Contents of ${name}`}
            />
          )
        )}
      </div>

      {!file.error && (
      <div className="flex min-h-9 items-center gap-2 border-t px-4 py-1.5 text-xs text-muted-foreground">
        {readOnly ? (
          <>
            <Lock className="size-3.5" /> Read-only
          </>
        ) : dirty ? (
          <span className="text-foreground">Unsaved changes · {saveKey} to save</span>
        ) : save.isSuccess ? (
          "Saved"
        ) : (
          "No changes"
        )}
      </div>
      )}
    </>
  );
}
