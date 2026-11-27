import { Plus, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@kipit-labs/kipitidy/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@kipit-labs/kipitidy/dialog";
import { FloatingInput } from "@kipit-labs/kipitidy/floating-input";
import { Segmented, SegmentedItem } from "@kipit-labs/kipitidy/segmented";
import { cn } from "@/lib/utils";
import { api, type KeyChanges } from "../../api";
import { ReviewDialog } from "./changes";
import { typeNames } from "./redis-types";

const newKeyColumns: Record<string, string[]> = { hash: ["field", "value"], list: ["value"], set: ["member"], zset: ["member", "score"] };

/** Creates a key with its first items, after the same review as any other save. */
export function NewKeyDialog({ open, onOpenChange, serviceId, onCreated }: { open: boolean; onOpenChange: (o: boolean) => void; serviceId: string; onCreated: (key: string) => void }) {
  const [name, setName] = useState("");
  const [type, setType] = useState("string");
  const [value, setValue] = useState("");
  const [rows, setRows] = useState<string[][]>([["", ""]]);
  const [reviewing, setReviewing] = useState(false);
  const cols = newKeyColumns[type] ?? [];
  const filled = rows.filter((r) => r.slice(0, cols.length).some((c) => c !== ""));
  const changes = (): KeyChanges => ({
    type,
    create: true,
    inserts:
      type === "string"
        ? [[value]]
        : filled.map((r) => (type === "list" ? ["end", r[0]] : type === "zset" ? [r[0], r[1] || "0"] : r.slice(0, cols.length))),
  });
  const close = (o: boolean) => {
    onOpenChange(o);
    if (!o) {
      setName("");
      setType("string");
      setValue("");
      setRows([["", ""]]);
    }
  };
  const ready = name.trim() !== "" && (type === "string" || filled.length > 0);

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className="flex max-h-[85vh] flex-col sm:max-w-lg">
        <form
          className="contents"
          onSubmit={(e) => {
            e.preventDefault();
            if (ready) setReviewing(true);
          }}
        >
          <DialogHeader>
            <DialogTitle>New key</DialogTitle>
            <DialogDescription>A key with its first value or items, written when you save. It never expires.</DialogDescription>
          </DialogHeader>
          <div className="min-h-0 space-y-4 overflow-y-auto">
            <FloatingInput label="Key" autoFocus autoComplete="off" spellCheck={false} value={name} onChange={(e) => setName(e.target.value)} inputClassName="font-mono" />
            <Segmented size="sm" value={type} onValueChange={setType} aria-label="Type">
              {["string", "hash", "list", "set", "zset"].map((t) => (
                <SegmentedItem key={t} value={t}>
                  {typeNames[t] ?? t}
                </SegmentedItem>
              ))}
            </Segmented>
            {type === "string" ? (
              <textarea
                aria-label="Value"
                spellCheck={false}
                value={value}
                onChange={(e) => setValue(e.target.value)}
                placeholder="Value"
                className="field-sizing-content min-h-24 w-full resize-none rounded-md border bg-background p-3 font-mono text-xs outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
              />
            ) : (
              <div className="space-y-1.5">
                {rows.map((r, i) => (
                  <div key={i} className="flex items-center gap-1.5">
                    {cols.map((c, j) => (
                      <input
                        key={c}
                        aria-label={`${c} ${i + 1}`}
                        placeholder={c === "score" ? "0" : c}
                        spellCheck={false}
                        value={r[j] ?? ""}
                        inputMode={c === "score" ? "decimal" : undefined}
                        onChange={(e) => setRows(rows.map((x, k) => (k === i ? x.map((y, m) => (m === j ? e.target.value : y)) : x)))}
                        className={cn(
                          "h-8 min-w-0 rounded-md border bg-background px-2 font-mono text-xs outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50",
                          c === "score" ? "w-24" : "flex-1",
                        )}
                      />
                    ))}
                    <Button type="button" variant="ghost" size="icon-xs" title="Remove this item" disabled={rows.length === 1} onClick={() => setRows(rows.filter((_, k) => k !== i))}>
                      <Trash2 />
                    </Button>
                  </div>
                ))}
                <Button type="button" variant="ghost" size="xs" onClick={() => setRows([...rows, ["", ""]])}>
                  <Plus /> Item
                </Button>
              </div>
            )}
          </div>
          <DialogFooter>
            <Button type="submit" disabled={!ready}>
              Review
            </Button>
          </DialogFooter>
        </form>
        <ReviewDialog
          open={reviewing}
          onOpenChange={setReviewing}
          atomic="They run at once, only if the key doesn't exist yet."
          run={(apply) => api.keyChanges(serviceId, name.trim(), changes(), apply)}
          onSaved={() => {
            toast.success(`${name.trim()} created`);
            onCreated(name.trim());
            close(false);
          }}
        />
      </DialogContent>
    </Dialog>
  );
}
