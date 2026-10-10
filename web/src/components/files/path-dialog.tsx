import { useState, type FormEvent, type ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";

/** Asks for one name or path (rename, new folder, move or copy destination). Closes when onSubmit resolves. */
export function PathDialog({
  open,
  onOpenChange,
  title,
  description,
  label,
  initial,
  submitLabel,
  validate,
  onSubmit,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: ReactNode;
  label: string;
  initial: string;
  submitLabel: string;
  /** An error to show for the value, or null when it's fine. */
  validate?: (value: string) => string | null;
  onSubmit: (value: string) => Promise<unknown>;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        {/* Remounted on each open so the field starts from initial. */}
        {open && (
          <PathForm
            title={title}
            description={description}
            label={label}
            initial={initial}
            submitLabel={submitLabel}
            validate={validate}
            onSubmit={async (v) => {
              await onSubmit(v);
              onOpenChange(false);
            }}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}

function PathForm({
  title,
  description,
  label,
  initial,
  submitLabel,
  validate,
  onSubmit,
}: {
  title: string;
  description?: ReactNode;
  label: string;
  initial: string;
  submitLabel: string;
  validate?: (value: string) => string | null;
  onSubmit: (value: string) => Promise<void>;
}) {
  const [value, setValue] = useState(initial);
  const [busy, setBusy] = useState(false);
  const problem = value.trim() === "" ? "" : (validate?.(value.trim()) ?? null);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (problem !== null || busy) return;
    setBusy(true);
    try {
      await onSubmit(value.trim());
    } catch {
      // The mutation's toast says why; keep the dialog open to fix it.
    } finally {
      setBusy(false);
    }
  };
  return (
    <form onSubmit={submit} className="contents">
      <DialogHeader>
        <DialogTitle>{title}</DialogTitle>
        {description && <DialogDescription>{description}</DialogDescription>}
      </DialogHeader>
      <div className="space-y-1.5">
        <FloatingInput
          label={label}
          autoFocus
          autoComplete="off"
          spellCheck={false}
          inputClassName="font-mono"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onFocus={(e) => {
            // Select the name without its extension, like a file manager.
            const dot = e.target.value.lastIndexOf(".");
            const slash = e.target.value.lastIndexOf("/");
            e.target.setSelectionRange(slash + 1, dot > slash + 1 ? dot : e.target.value.length);
          }}
        />
        {problem && <p className="text-xs text-destructive">{problem}</p>}
      </div>
      <DialogFooter>
        <Button type="submit" disabled={problem !== null || busy}>
          {submitLabel}
        </Button>
      </DialogFooter>
    </form>
  );
}
