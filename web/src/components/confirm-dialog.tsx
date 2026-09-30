import { TriangleAlert } from "lucide-react";
import { useState, type FormEvent, type ReactNode } from "react";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { FloatingInput } from "@/components/ui/floating-input";

/**
 * Asks before a destructive action. With typeToConfirm, the user must type
 * that name first; onConfirm receives what they typed.
 */
export function ConfirmDialog({
  trigger,
  title,
  description,
  confirmLabel = "Delete",
  destructive = true,
  typeToConfirm,
  onConfirm,
}: {
  trigger: ReactNode;
  title: string;
  description?: ReactNode;
  confirmLabel?: string;
  destructive?: boolean;
  typeToConfirm?: string;
  onConfirm: (typed: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [typed, setTyped] = useState("");
  const ok = !typeToConfirm || typed === typeToConfirm;

  const onOpenChange = (next: boolean) => {
    setOpen(next);
    if (!next) setTyped("");
  };
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (!ok) return;
    onConfirm(typed);
    onOpenChange(false);
  };

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogTrigger asChild>{trigger}</AlertDialogTrigger>
      <AlertDialogContent>
        <form onSubmit={onSubmit} className="contents">
          <AlertDialogHeader>
            {destructive && (
              <AlertDialogMedia className="bg-destructive/10 text-destructive">
                <TriangleAlert />
              </AlertDialogMedia>
            )}
            <AlertDialogTitle>{title}</AlertDialogTitle>
            {description && <AlertDialogDescription>{description}</AlertDialogDescription>}
          </AlertDialogHeader>
          {typeToConfirm && (
            <FloatingInput
              label={`Type "${typeToConfirm}" to confirm`}
              autoComplete="off"
              autoFocus
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
            />
          )}
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <Button
              type="submit"
              disabled={!ok}
              className={destructive ? "bg-destructive text-white hover:bg-destructive/80" : undefined}
            >
              {confirmLabel}
            </Button>
          </AlertDialogFooter>
        </form>
      </AlertDialogContent>
    </AlertDialog>
  );
}
