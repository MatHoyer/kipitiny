import { Lock, LockOpen, TriangleAlert } from "lucide-react";
import { useState } from "react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Segmented, SegmentedItem } from "@/components/ui/segmented";

/**
 * Read only, or writes allowed after a confirmation. description says what
 * writing then does; disabled, why writes can't be allowed here.
 */
export function WriteMode({
  write,
  onChange,
  description = "What you run then changes the database right away. Only a restore from a backup undoes it.",
  disabled,
  className,
}: {
  write: boolean;
  onChange: (w: boolean) => void;
  description?: string;
  disabled?: string;
  className?: string;
}) {
  const [asking, setAsking] = useState(false);
  return (
    <>
      <Segmented
        size="sm"
        value={write ? "write" : "read"}
        onValueChange={(v) => (v === "write" ? setAsking(true) : v === "read" && onChange(false))}
        aria-label="Write mode"
        title={disabled}
        className={className}
      >
        <SegmentedItem value="read">
          <Lock /> Read only
        </SegmentedItem>
        <SegmentedItem
          value="write"
          disabled={!!disabled}
          className="aria-checked:bg-destructive/10 aria-checked:text-destructive dark:aria-checked:bg-destructive/15 dark:aria-checked:text-destructive"
        >
          <LockOpen /> Allow writes
        </SegmentedItem>
      </Segmented>
      <AlertDialog open={asking} onOpenChange={setAsking}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogMedia className="bg-destructive/10 text-destructive">
              <TriangleAlert />
            </AlertDialogMedia>
            <AlertDialogTitle>Allow writes?</AlertDialogTitle>
            <AlertDialogDescription>{description}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Stay read only</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={() => onChange(true)}>
              Allow writes
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
