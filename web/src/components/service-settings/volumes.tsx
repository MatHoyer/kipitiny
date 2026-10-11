import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Plus, Trash2 } from "lucide-react";
import { useId, useState, type FormEvent } from "react";
import { toast } from "sonner";
import { Empty, Section } from "@/components/common";
import { SaveBar } from "@/components/save-bar";
import { Button } from "@/components/ui/button";
import { FloatingInput } from "@/components/ui/floating-input";
import { api, type Service as ServiceT, type Volume } from "../../api";
import { Applies } from "./parts";

const volumeNameRe = /^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$/;

/** Named volumes of an app: kept across deploys, shared by its replicas. */
export function VolumesCard({ svc }: { svc: ServiceT }) {
  const qc = useQueryClient();
  const [rows, setRows] = useState<Volume[]>(svc.volumes);
  const [preBackup, setPreBackup] = useState(svc.preBackup);
  const dirty = JSON.stringify(rows) !== JSON.stringify(svc.volumes) || preBackup !== svc.preBackup;
  const formId = useId();
  const save = useMutation({
    meta: { error: "Couldn't save the volumes" },
    mutationFn: () =>
      api.updateService(svc.id, {
        volumes: rows.map((v) => ({ name: v.name.trim(), path: v.path.trim() })),
        preBackup: rows.length > 0 ? preBackup.trim() : "",
      }),
    onSuccess: (updated) => {
      qc.setQueryData(["service", svc.id], updated);
      setRows(updated.volumes);
      setPreBackup(updated.preBackup);
      toast.success("Volumes saved", { description: "Deploy to apply." });
    },
  });
  const reset = () => {
    setRows(svc.volumes);
    setPreBackup(svc.preBackup);
  };
  const set = (i: number, field: keyof Volume) => (e: { target: { value: string } }) =>
    setRows(rows.map((v, j) => (j === i ? { ...v, [field]: e.target.value } : v)));
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <Section
      title={<Applies title="Volumes" when="deploy" />}
      description="Data that outlives deploys, e.g. uploads. Every replica mounts the same volume. A volume removed here keeps its data until the service is deleted."
      actions={
        <Button type="button" size="sm" variant="outline" onClick={() => setRows([...rows, { name: "", path: "" }])}>
          <Plus data-icon="inline-start" />
          Add volume
        </Button>
      }
    >
      {/* Always rendered: the save bar submits it, even to remove the last volume. */}
      <form id={formId} onSubmit={onSubmit} className="space-y-3">
        {rows.length === 0 && <Empty>No volumes: files written in the containers are lost on every deploy.</Empty>}
        {rows.map((v, i) => {
          const name = v.name.trim();
          const badName = name !== "" && !volumeNameRe.test(name);
          const isNew = name !== "" && !svc.volumes.some((o) => o.name === name);
          return (
            <div key={i} className="flex items-start gap-2">
              <FloatingInput
                label="Name"
                required
                value={v.name}
                onChange={set(i, "name")}
                placeholder="uploads"
                className="w-40 shrink-0 sm:w-56"
                inputClassName="font-mono"
                aria-invalid={badName}
                description={badName ? "Lowercase letters, digits and dashes." : isNew ? "New volume, created empty." : undefined}
              />
              <FloatingInput
                label="Mount path"
                required
                value={v.path}
                onChange={set(i, "path")}
                placeholder="/app/uploads"
                className="min-w-0 flex-1"
                inputClassName="font-mono"
              />
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                title="Remove"
                aria-label={`Remove ${v.name || "volume"}`}
                className="mt-3.5 text-muted-foreground hover:text-destructive"
                onClick={() => setRows(rows.filter((_, j) => j !== i))}
              >
                <Trash2 />
              </Button>
            </div>
          );
        })}
        {rows.length > 0 && (
          <FloatingInput
            label="Pre-backup command"
            value={preBackup}
            onChange={(e) => setPreBackup(e.target.value)}
            placeholder="./bin/flush"
            inputClassName="font-mono"
            description="Runs (sh -c) in a running replica before each backup of the volumes, e.g. to flush to disk. Optional."
          />
        )}
      </form>
      <SaveBar form={formId} dirty={dirty} saving={save.isPending} onReset={reset} label="Save volumes" />
    </Section>
  );
}
