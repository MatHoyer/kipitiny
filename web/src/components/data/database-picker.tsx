import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Database, Plus } from "lucide-react";
import { useState, type FormEvent } from "react";
import { ErrorText } from "@/components/common";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";
import { api, type PgDatabase } from "../../api";

/** Picks one of the instance's databases. Shown even with a single one, so you always see where you are. */
export function DatabasePicker({ databases, value, onChange, className }: { databases: PgDatabase[]; value: string; onChange: (d: string) => void; className?: string }) {
  if (databases.length === 0) return null;
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger size="sm" aria-label="Database" className={cn("font-mono text-xs", className)}>
        <Database className="text-muted-foreground" />
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {databases.map((d) => (
          <SelectItem key={d.name} value={d.name} className="font-mono text-xs">
            <span className="mr-3 flex-1">{d.name}</span>
            {d.main && <span className="text-muted-foreground">main</span>}
            <span className="text-muted-foreground">{formatBytes(d.bytes)}</span>
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

const nameRe = /^[a-z_][a-z0-9_]{0,62}$/;
const collectionRe = /^[A-Za-z_][A-Za-z0-9_.-]{0,119}$/;

/**
 * Creates a database in the instance, owned by the service's user. A MongoDB
 * one (mongo) starts with a collection: without one it doesn't exist.
 */
export function NewDatabaseButton({ serviceId, mongo, onCreated }: { serviceId: string; mongo?: boolean; onCreated: (name: string) => void }) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [collection, setCollection] = useState("");
  const create = useMutation({
    meta: { error: false },
    mutationFn: () => api.createPgDatabase(serviceId, name, mongo ? collection : ""),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["data", serviceId, "databases"] });
      onCreated(name);
      onOpenChange(false);
    },
  });
  const onOpenChange = (next: boolean) => {
    setOpen(next);
    if (!next) {
      setName("");
      setCollection("");
      create.reset();
    }
  };
  const valid = nameRe.test(name) && (!mongo || (collectionRe.test(collection) && !collection.startsWith("system.")));
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (valid) create.mutate();
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button variant="outline" size="icon-sm" title="New database" aria-label="New database">
          <Plus />
        </Button>
      </DialogTrigger>
      {/* No focus back on the trigger: it would pop its tooltip. */}
      <DialogContent className="sm:max-w-md" onCloseAutoFocus={(e) => e.preventDefault()}>
        <form onSubmit={onSubmit} className="contents">
          <DialogHeader>
            <DialogTitle>New database</DialogTitle>
            <DialogDescription>
              {mongo ? (
                <>
                  A database in this instance with its first collection: MongoDB only keeps a database that holds one. Apps reach it
                  with the same credentials and host, ending the connection URL with its name.
                </>
              ) : (
                <>
                  An empty database in this instance, owned by the service&apos;s user. Apps reach it with the same credentials and
                  host, ending the connection URL with its name.
                </>
              )}
            </DialogDescription>
          </DialogHeader>
          <FloatingInput
            label="Name"
            autoFocus
            autoComplete="off"
            value={name}
            onChange={(e) => setName(e.target.value)}
            inputClassName="font-mono"
            description="Lowercase letters, digits and _, starting with a letter or _."
          />
          {mongo && (
            <FloatingInput
              label="First collection"
              autoComplete="off"
              value={collection}
              onChange={(e) => setCollection(e.target.value)}
              inputClassName="font-mono"
              description="Letters, digits, _ . and -, starting with a letter or _."
            />
          )}
          <ErrorText error={create.error} />
          <DialogFooter>
            <Button type="submit" loading={create.isPending} disabled={!valid}>
              Create database
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
