import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useId, useState, type FormEvent } from "react";
import { toast } from "sonner";
import { CheckboxField, Mono, Section } from "@/components/common";
import { SaveBar } from "@/components/save-bar";
import { FloatingSelect } from "@/components/ui/floating-select";
import { api, type Service as ServiceT } from "../../api";
import { Applies } from "./parts";

/** Host network and Docker socket, for agents that report on or manage the server (Beszel, node-exporter…). */
export function HostAccessCard({ svc }: { svc: ServiceT }) {
  const qc = useQueryClient();
  const initial = { hostNetwork: svc.hostNetwork, dockerSocket: svc.dockerSocket };
  const [form, setForm] = useState(initial);
  const dirty = form.hostNetwork !== initial.hostNetwork || form.dockerSocket !== initial.dockerSocket;
  const formId = useId();
  const blocked = form.hostNetwork && (!!svc.domain || svc.publishedPorts.length > 0);
  const save = useMutation({
    meta: { error: "Couldn't save host access" },
    mutationFn: () =>
      api.updateService(svc.id, {
        ...form,
        // An app in the host network runs one replica.
        ...(form.hostNetwork && svc.replicas !== 1 ? { replicas: 1 } : {}),
      }),
    onSuccess: (updated) => {
      qc.setQueryData(["service", svc.id], updated);
      setForm({ hostNetwork: updated.hostNetwork, dockerSocket: updated.dockerSocket });
      toast.success("Host access saved", { description: "Deploy to apply." });
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <Section
      title={<Applies title="Host access" when="deploy" />}
      description="For agents that report on or manage the server, such as a Beszel agent. Only give it to images you trust: the Docker socket, even read-only, gives control of the server."
    >
      <form id={formId} onSubmit={onSubmit} className="space-y-4">
        <CheckboxField
          label="Host network"
          description="The app shares the server's network: it sees the host's interfaces and listens on its ports directly. No domain, published ports or project network (other services can't reach it by name), one replica, and deploys stop the old container first."
          checked={form.hostNetwork}
          onCheckedChange={(v) => setForm({ ...form, hostNetwork: v })}
        />
        {blocked && <p className="text-sm text-destructive">Remove the domain and published ports first.</p>}
        <FloatingSelect
          label="Docker socket"
          value={form.dockerSocket || "none"}
          onValueChange={(v) => setForm({ ...form, dockerSocket: v === "none" ? "" : (v as "ro" | "rw") })}
          options={[
            { value: "none", label: "Not mounted" },
            { value: "ro", label: "Read-only (container stats, e.g. Beszel)" },
            { value: "rw", label: "Read-write (manages containers)" },
          ]}
          description={<>Mounted at <Mono>/var/run/docker.sock</Mono>.</>}
        />
      </form>
      <SaveBar form={formId} dirty={dirty && !blocked} saving={save.isPending} onReset={() => setForm(initial)} label="Save host access" />
    </Section>
  );
}
