import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useId, useState, type FormEvent } from "react";
import { toast } from "sonner";
import { Section } from "@/components/common";
import { SaveBar } from "@/components/save-bar";
import { ServiceIcon, useServiceLogos, type Logos } from "@/components/service-icon";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingSelect } from "@/components/ui/floating-select";
import { cn } from "@/lib/utils";
import { api, isDatabase, type Service as ServiceT } from "../../api";
import { Applies } from "./parts";

/** The settings form's fields, as saved. */
const settingsForm = (s: ServiceT) => ({
  image: s.image,
  icon: s.icon || AUTO_ICON,
  replicas: String(s.replicas),
  memory: s.memoryMb ? String(s.memoryMb) : "",
  cpus: s.cpus ? String(s.cpus) : "",
  healthPath: s.healthPath,
  preDeploy: s.preDeploy,
  stopGrace: s.stopGraceSeconds ? String(s.stopGraceSeconds) : "",
});

/** Radix selects can't hold "": this stands for an empty icon. */
const AUTO_ICON = "auto";

/** Automatic (from the image) or a known logo; an unknown saved icon stays listed. */
function iconOptions(svc: ServiceT, image: string, { logos }: Logos) {
  const option = (value: string, icon: string | undefined, label: string) => ({
    value,
    label: (
      <span className="flex items-center gap-2">
        <ServiceIcon service={{ icon, kind: svc.kind, image: icon ? undefined : image }} className="size-4 shrink-0" />
        {label}
      </span>
    ),
  });
  const names = Object.keys(logos);
  if (svc.icon && !names.includes(svc.icon)) names.push(svc.icon);
  return [
    option(AUTO_ICON, undefined, "Automatic, from the image"),
    ...names.map((name) => option(name, name, logos[name]?.label ?? name)),
  ];
}

/** Image, icon, resources and how deploys roll out. */
export function GeneralCard({ svc }: { svc: ServiceT }) {
  const qc = useQueryClient();
  const isDb = isDatabase(svc.kind);
  const [form, setForm] = useState(() => settingsForm(svc));
  const dirty = JSON.stringify(form) !== JSON.stringify(settingsForm(svc));
  const formId = useId();
  const logos = useServiceLogos();
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });

  const save = useMutation({
    meta: { error: "Couldn't save the settings" },
    mutationFn: () =>
      api.updateService(
        svc.id,
        isDb
          ? { image: form.image.trim(), memoryMb: Number(form.memory) || 0, cpus: Number(form.cpus) || 0 }
          : {
              image: form.image.trim(),
              icon: form.icon === AUTO_ICON ? "" : form.icon,
              replicas: Number(form.replicas) || 1,
              memoryMb: Number(form.memory) || 0,
              cpus: Number(form.cpus) || 0,
              healthPath: form.healthPath.trim(),
              preDeploy: form.preDeploy.trim(),
              stopGraceSeconds: Number(form.stopGrace) || 0,
            },
      ),
    onSuccess: (updated) => {
      qc.setQueryData(["service", svc.id], updated);
      setForm(settingsForm(updated));
      toast.success("Settings saved", {
        description: isDb ? "Deploy to apply." : "Replica count applies now; deploy to apply other changes.",
      });
    },
  });

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <Section
      title={<Applies title="Image & runtime" when="deploy" />}
      description={isDb ? undefined : "Except the icon and the replica count, which apply right away."}
    >
      <form id={formId} onSubmit={onSubmit} className={cn("grid gap-4", isDb ? "sm:grid-cols-2" : "sm:grid-cols-3")}>
        <FloatingInput
          label="Image"
          required
          value={form.image}
          onChange={set("image")}
          className="col-span-full"
          description={imageHints[svc.kind]}
        />
        {!isDb && (
          <>
            <FloatingSelect
              label="Icon"
              value={form.icon}
              onValueChange={(icon) => setForm({ ...form, icon })}
              options={iconOptions(svc, form.image, logos)}
              className="col-span-full"
            />
            <FloatingInput
              label="Replicas"
              type="number"
              min={1}
              max={10}
              value={form.replicas}
              onChange={set("replicas")}
              disabled={svc.publishedPorts.length > 0 || svc.hostNetwork}
              description={
                svc.publishedPorts.length > 0
                  ? "One: two containers can't publish the same port."
                  : svc.hostNetwork
                    ? "One: the app runs in the host's network."
                    : undefined
              }
            />
          </>
        )}
        <FloatingInput
          label="Memory (MB)"
          type="number"
          min={0}
          step={64}
          value={form.memory}
          onChange={set("memory")}
          description={isDb ? undefined : "Empty for no limit."}
        />
        <FloatingInput
          label="CPUs"
          type="number"
          min={0}
          step={0.25}
          value={form.cpus}
          onChange={set("cpus")}
          placeholder="0.5"
          description="Cores, e.g. 0.5. Empty for no limit."
        />
        {!isDb && (
          <>
            <FloatingInput
              label="Health path"
              value={form.healthPath}
              onChange={set("healthPath")}
              placeholder="/healthz"
              className="col-span-full"
              description="New replicas get traffic once this answers < 400. Empty: port accepts connections."
            />
            <FloatingInput
              label="Pre-deploy command"
              value={form.preDeploy}
              onChange={set("preDeploy")}
              placeholder="npm run migrate"
              inputClassName="font-mono"
              className="col-span-full"
              description="Runs once (sh -c) before replicas start, e.g. migrations."
            />
            <FloatingInput
              label="Stop grace period (s)"
              type="number"
              min={0}
              max={600}
              value={form.stopGrace}
              onChange={set("stopGrace")}
              placeholder="10"
              description="Time to exit after SIGTERM before being killed, e.g. to save a game world. Empty: 10 s."
            />
          </>
        )}
      </form>
      <SaveBar form={formId} dirty={dirty} saving={save.isPending} onReset={() => setForm(settingsForm(svc))} />
    </Section>
  );
}

/** What changing a database's image can and can't do. */
const imageHints: Partial<Record<ServiceT["kind"], string>> = {
  postgres: "Minor upgrades only; major versions need a dump and restore.",
  mysql: "Upgrades apply on start; going back to an older version needs a dump and restore.",
  mariadb: "Upgrades apply on start; going back to an older version needs a dump and restore.",
  mongodb: "One major version at a time, forward only; older versions can't read newer data.",
};
