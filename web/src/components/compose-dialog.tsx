import { useMutation, useQuery } from "@tanstack/react-query";
import { Download, FileCode, KeyRound } from "lucide-react";
import { useState, type FormEvent } from "react";
import { CopyButton, ErrorText, Loading } from "@/components/common";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api } from "../api";

/** Shows a project (or one service) as a docker-compose file, and exports it
 * with its secrets to move it to another kipitiny. */
export function ComposeDialog({ projectId, service }: { projectId: string; service?: string }) {
  const [open, setOpen] = useState(false);
  const [tab, setTab] = useState("view");
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm" variant="outline">
          <FileCode data-icon="inline-start" />
          Compose
        </Button>
      </DialogTrigger>
      <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>{service ? `${service} as compose` : "Project as compose"}</DialogTitle>
          <DialogDescription>
            Standard docker-compose; kipitiny settings live in <code>x-kipitiny</code> blocks, which Docker Compose ignores.
          </DialogDescription>
        </DialogHeader>
        <Tabs value={tab} onValueChange={setTab}>
          <TabsList variant="line">
            <TabsTrigger value="view">View</TabsTrigger>
            <TabsTrigger value="export">Export with secrets</TabsTrigger>
          </TabsList>
          <TabsContent value="view">{open && <ComposeView projectId={projectId} service={service} />}</TabsContent>
          <TabsContent value="export">
            <ExportBundle projectId={projectId} service={service} onDone={() => setOpen(false)} />
          </TabsContent>
        </Tabs>
      </DialogContent>
    </Dialog>
  );
}

function ComposeView({ projectId, service }: { projectId: string; service?: string }) {
  const compose = useQuery({ queryKey: ["compose", projectId, service], queryFn: () => api.compose(projectId, service) });
  if (compose.error) return <ErrorText error={compose.error} />;
  if (compose.data === undefined) return <Loading />;
  return (
    <div className="space-y-3">
      <div className="relative">
        <pre className="max-h-[55svh] overflow-auto rounded-md border bg-muted/40 p-3 font-mono text-xs leading-relaxed">{compose.data}</pre>
        <div className="absolute top-2 right-2">
          <CopyButton value={compose.data} />
        </div>
      </div>
      <p className="text-xs text-muted-foreground">
        Secrets are <code>{"${NAME}"}</code> variables without values. Data (databases, volumes) moves with a backup restored on the new
        server.
      </p>
      <Button size="sm" variant="outline" asChild>
        <a href={api.composeUrl(projectId, service)} download>
          <Download data-icon="inline-start" />
          Download compose.yaml
        </a>
      </Button>
    </div>
  );
}

function ExportBundle({ projectId, service, onDone }: { projectId: string; service?: string; onDone: () => void }) {
  const [password, setPassword] = useState("");
  const exportBundle = useMutation({
    mutationFn: () => api.exportBundle(projectId, password, service),
    onSuccess: (blob) => {
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = "kipitiny-export.zip";
      a.click();
      URL.revokeObjectURL(url);
      setPassword("");
      onDone();
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    exportBundle.mutate();
  };
  return (
    <form onSubmit={onSubmit} className="space-y-4">
      <p className="text-sm text-muted-foreground">
        Downloads <code>compose.yaml</code> and a <code>.env</code> holding every secret value (env secrets, project secrets, database
        passwords, basic auth hashes). Import both on another kipitiny, or run them with{" "}
        <code>docker compose --env-file .env up</code>. Keep the file safe.
      </p>
      <FloatingInput
        label="Your password"
        type="password"
        autoComplete="current-password"
        required
        value={password}
        onChange={(e) => setPassword(e.target.value)}
      />
      <ErrorText error={exportBundle.error} />
      <DialogFooter>
        <Button type="submit" disabled={!password || exportBundle.isPending}>
          <KeyRound data-icon="inline-start" />
          Export with secrets
        </Button>
      </DialogFooter>
    </form>
  );
}
