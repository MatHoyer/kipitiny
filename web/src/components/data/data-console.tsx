import { useMutation } from "@tanstack/react-query";
import { Play, TriangleAlert } from "lucide-react";
import { useState } from "react";
import { CheckboxField, ErrorText } from "@/components/common";
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
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { api, type DatabaseKind } from "../../api";
import { DataTable } from "./data-table";

const placeholders: Record<DatabaseKind, string> = {
  postgres: "SELECT * FROM users ORDER BY created_at DESC LIMIT 10",
  redis: "HGETALL user:1",
};

/** Runs SQL or a redis command. Read-only unless writes are allowed, which asks first. */
export function DataConsole({ serviceId, kind }: { serviceId: string; kind: DatabaseKind }) {
  const [query, setQuery] = useState("");
  const [write, setWrite] = useState(false);
  const [asking, setAsking] = useState(false);
  const run = useMutation({
    meta: { error: false },
    mutationFn: () => api.dataConsole(serviceId, query, write),
  });
  const submit = () => {
    if (!query.trim() || run.isPending) return;
    run.mutate();
  };
  const res = run.data;

  return (
    <div className="space-y-3">
      <form
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault();
          submit();
        }}
      >
        <Textarea
          aria-label={kind === "postgres" ? "SQL" : "Command"}
          className="min-h-28 font-mono text-sm"
          placeholder={placeholders[kind]}
          spellCheck={false}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
              e.preventDefault();
              submit();
            }
          }}
        />
        <div className="flex flex-wrap items-center justify-between gap-3">
          <CheckboxField
            label="Allow writes"
            description={
              kind === "postgres"
                ? "Off: the session is read-only. On: the whole input runs in one transaction."
                : "Off: commands that change data or the server are refused."
            }
            checked={write}
            onCheckedChange={(on) => (on ? setAsking(true) : setWrite(false))}
          />
          <Button type="submit" variant={write ? "destructive" : "default"} loading={run.isPending} disabled={!query.trim()}>
            {write ? <TriangleAlert /> : <Play />} Run
            <kbd className="ml-1 hidden text-xs opacity-60 sm:inline">Ctrl+Enter</kbd>
          </Button>
        </div>
      </form>
      <AlertDialog open={asking} onOpenChange={setAsking}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogMedia className="bg-destructive/10 text-destructive">
              <TriangleAlert />
            </AlertDialogMedia>
            <AlertDialogTitle>Allow writes?</AlertDialogTitle>
            <AlertDialogDescription>
              What you run then changes the database right away. There is no undo but a restore from a backup.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction className="bg-destructive text-white hover:bg-destructive/80" onClick={() => setWrite(true)}>
              Allow writes
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      <ErrorText error={run.error} />
      {res &&
        (res.columns ? (
          <>
            <DataTable columns={res.columns.map((name) => ({ name }))} rows={res.rows ?? []} truncated={res.truncated} />
            <p className="text-xs text-muted-foreground">
              {res.rows?.length ?? 0} rows{res.more && " shown; the rest was cut. Add a LIMIT or export the table."}
            </p>
          </>
        ) : (
          <pre className="max-h-[60vh] overflow-auto rounded-lg border bg-muted p-3 font-mono text-xs whitespace-pre-wrap">
            {res.output}
            {res.more && "\n… output cut"}
          </pre>
        ))}
    </div>
  );
}
