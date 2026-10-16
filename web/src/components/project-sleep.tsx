import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Moon } from "lucide-react";
import { useState, type FormEvent } from "react";
import { toast } from "sonner";
import { Section, Tag } from "@/components/common";
import { Button } from "@/components/ui/button";
import { FloatingInput } from "@/components/ui/floating-input";
import { api, type Project, type Service } from "../api";

/** Why the project can't sleep: only a request through Traefik wakes it. */
export function sleepBlocker(services: Service[]) {
  const around = services.find((s) => s.publishedPorts.length > 0 || s.hostNetwork);
  if (around)
    return `${around.name} ${around.hostNetwork ? "uses the host network" : "has published ports"}: its traffic can't wake the project.`;
  if (!services.some((s) => s.domain && s.port)) return "Only a project with an app on a domain can sleep: a request to it wakes the project.";
}

const minutes = (p: Project) => (p.sleepAfterSeconds ? String(p.sleepAfterSeconds / 60) : "");

/** The project's sleep mode: every service stops when its apps get no request. */
export function SleepCard({ project, services, disabled }: { project: Project; services: Service[]; disabled?: boolean }) {
  const qc = useQueryClient();
  const [value, setValue] = useState(() => minutes(project));
  const blocker = project.sleepAfterSeconds ? undefined : sleepBlocker(services);
  const asleep = services.some((s) => s.sleeping);
  const save = useMutation({
    meta: { error: "Couldn't save sleep mode" },
    mutationFn: (secs: number) => api.setProjectSleep(project.id, secs),
    onSuccess: (p) => {
      qc.setQueryData(["project", project.id], p);
      qc.invalidateQueries({ queryKey: ["services", project.id] });
      setValue(minutes(p));
      toast.success(p.sleepAfterSeconds ? "Sleep mode on" : "Sleep mode off");
    },
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate(Math.round((Number(value) || 0) * 60));
  };
  return (
    <Section
      id="sleep"
      title={
        <span className="flex items-center gap-2">
          Sleep mode
          {asleep && (
            <Tag className="flex items-center gap-1">
              <Moon className="size-3 text-blue-500" />
              Asleep
            </Tag>
          )}
        </span>
      }
      description={
        <>
          After this long without a request to any of its apps, every service stops, databases and workers too; the next request starts
          them again behind a short "starting" page. Background jobs don't run while it sleeps. A deploy, a start, a backup or a data
          query wakes it too.
        </>
      }
    >
      <form onSubmit={submit} className="flex items-start gap-2">
        <FloatingInput
          label="Sleep after idle (min)"
          type="number"
          min={5}
          max={43200}
          className="w-full max-w-sm"
          placeholder="Never"
          disabled={disabled || !!blocker}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          description={blocker ?? "Empty: never."}
        />
        <Button type="submit" variant="outline" disabled={disabled || !!blocker || value === minutes(project)} loading={save.isPending}>
          Save
        </Button>
      </form>
    </Section>
  );
}
