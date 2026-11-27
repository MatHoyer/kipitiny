import { Pencil, Trash2, Undo2 } from "lucide-react";
import { useState, type ReactNode } from "react";
import { CopyButton } from "@kipit-labs/kipitidy/copy-button";
import { Button } from "@kipit-labs/kipitidy/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@kipit-labs/kipitidy/dialog";
import { cn } from "@/lib/utils";

/** A filter or sort as Extended JSON, applied on Enter. */
export function JsonField({
  icon,
  label,
  placeholder,
  value,
  onApply,
  className,
}: {
  icon: ReactNode;
  label: string;
  placeholder: string;
  value: string;
  onApply: (v: string) => void;
  className?: string;
}) {
  const [text, setText] = useState(value);
  const dirty = text.trim() !== value;
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onApply(text.trim());
      }}
      className={cn(
        "flex h-8 items-center gap-1.5 rounded-md border bg-background px-2 focus-within:border-ring focus-within:ring-[3px] focus-within:ring-ring/50 [&_svg]:size-3.5 [&_svg]:text-muted-foreground",
        dirty && "border-dashed",
        className,
      )}
      title={`${label}: Extended JSON, applied with Enter`}
    >
      {icon}
      <input
        aria-label={label}
        placeholder={placeholder}
        value={text}
        spellCheck={false}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => e.key === "Escape" && setText(value)}
        className="min-w-0 flex-1 bg-transparent font-mono text-xs outline-none placeholder:text-muted-foreground/60"
      />
    </form>
  );
}

/**
 * One document, indented; a cut one stays as the server sent it. While
 * editing, its pending state shows: added, edited (which fields), deleted.
 */
export function Document({
  json,
  cut,
  state,
  changed,
  locked,
  onEdit,
  onDelete,
}: {
  json: string;
  cut: boolean;
  state?: "new" | "edited" | "deleted";
  changed?: string[];
  locked?: string;
  onEdit?: () => void;
  onDelete?: () => void;
}) {
  let pretty = json;
  if (!cut) {
    try {
      pretty = JSON.stringify(JSON.parse(json), null, 2);
    } catch {
      // Shown as sent.
    }
  }
  const editable = !!(onEdit || onDelete || locked);
  return (
    <li
      className={cn(
        "group relative px-4 py-3",
        state === "new" && "bg-emerald-500/10 shadow-[inset_2px_0_0] shadow-emerald-500",
        state === "edited" && "bg-amber-500/10 shadow-[inset_2px_0_0] shadow-amber-500",
        state === "deleted" && "bg-destructive/5 text-muted-foreground line-through shadow-[inset_2px_0_0] shadow-destructive",
      )}
    >
      {changed && changed.length > 0 && (
        <p className="mb-1.5 font-mono text-xs text-amber-600 dark:text-amber-400">
          edited: {changed.join(", ")}
        </p>
      )}
      <pre className={cn("overflow-x-auto font-mono text-xs leading-relaxed whitespace-pre-wrap", editable && "pr-24")}>{pretty}</pre>
      {cut && <p className="mt-1 text-xs text-muted-foreground">Cut at 4 KiB: run a projection in the console to see the rest.</p>}
      <span
        className={cn(
          "absolute top-2 right-2 flex items-center transition-opacity",
          !editable && "opacity-0 group-hover:opacity-100 focus-within:opacity-100",
        )}
      >
        {state !== "deleted" && onEdit && (
          <Button variant="ghost" size="icon-xs" title={state === "new" ? "Edit this new document" : "Edit this document"} onClick={onEdit}>
            <Pencil />
          </Button>
        )}
        {onDelete && (
          <Button
            variant="ghost"
            size="icon-xs"
            title={state === "deleted" ? "Keep this document" : state === "new" ? "Remove this new document" : "Delete this document"}
            onClick={onDelete}
            className={cn(state !== "deleted" && "hover:text-destructive")}
          >
            {state === "deleted" ? <Undo2 /> : <Trash2 />}
          </Button>
        )}
        {locked && <span className="px-1 text-xs text-muted-foreground" title={locked}>locked</span>}
        {state !== "new" && <CopyButton value={json} label="Copy as JSON" />}
      </span>
    </li>
  );
}

/** Edits a document as relaxed Extended JSON; Done stages it, the collection's Save writes it. */
export function DocEditor({
  editing,
  onClose,
  onDone,
}: {
  editing: { id?: string; insert?: number; text: string } | null;
  onClose: () => void;
  /** Returns why the text can't be staged. */
  onDone: (text: string) => string | undefined;
}) {
  const [text, setText] = useState(editing?.text ?? "");
  const [error, setError] = useState<string>();
  const isNew = editing?.id === undefined;
  const done = () => {
    const why = onDone(text);
    if (why) setError(why);
    else onClose();
  };
  return (
    <Dialog open={!!editing} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="flex max-h-[85vh] flex-col gap-0 p-0 sm:max-w-2xl">
        <DialogHeader className="border-b p-4">
          <DialogTitle className="font-mono text-sm">{isNew ? "New document" : `_id ${editing?.id}`}</DialogTitle>
          <DialogDescription>
            {isNew
              ? "Extended JSON, as shown: { \"$date\": \"…\" } for a date, { \"$oid\": \"…\" } for an ObjectId. Without an _id, MongoDB adds one."
              : "Extended JSON, as shown. Only the top-level fields you change are saved; the others keep their exact types."}
          </DialogDescription>
        </DialogHeader>
        <div className="min-h-0 flex-1 overflow-y-auto p-4">
          <textarea
            autoFocus
            aria-label="Document"
            spellCheck={false}
            value={text}
            onChange={(e) => {
              setText(e.target.value);
              setError(undefined);
            }}
            onKeyDown={(e) => e.key === "Enter" && (e.ctrlKey || e.metaKey) && done()}
            className="field-sizing-content min-h-48 w-full resize-none rounded-md border bg-background p-3 font-mono text-xs leading-relaxed outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
          />
          {error && <p className="mt-2 text-sm text-destructive">{error}</p>}
        </div>
        <DialogFooter className="m-0">
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={done}>
            Done
            <kbd className="ml-1 hidden font-sans text-[0.7rem] opacity-60 sm:inline">Ctrl ↵</kbd>
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
