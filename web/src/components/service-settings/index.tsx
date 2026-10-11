import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState, type ReactNode } from "react";
import { MaintenanceCard } from "@/components/maintenance";
import { RenameCard } from "@/components/rename-card";
import { DirtyContext } from "@/components/save-bar";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useTab } from "@/hooks/use-tab";
import { api, httpRouted, isDatabase, type Service as ServiceT } from "../../api";
import { AccessCard } from "./access";
import { GeneralCard } from "./general";
import { HostAccessCard } from "./host-access";
import { DomainCard, NetworksCard, PortsCard } from "./networking";
import { GitOwned } from "./parts";
import { VolumesCard } from "./volumes";

const sectionIds = ["general", "networking", "access", "storage", "maintenance", "advanced"] as const;
type SectionId = (typeof sectionIds)[number];

/**
 * The service's settings, one section at a time (?section=). Every section stays
 * mounted so unsaved edits survive switching; its tab shows a dot meanwhile.
 */
export function ServiceSettings({
  svc,
  projectName,
  gitPath,
  danger,
}: {
  svc: ServiceT;
  projectName?: string;
  /** The compose file a git project follows: the cards it owns are read-only. */
  gitPath?: string;
  /** Last, in Advanced. */
  danger: ReactNode;
}) {
  const qc = useQueryClient();
  const isDb = isDatabase(svc.kind);
  const app = svc.kind === "app";
  const http = app && httpRouted(svc);
  const sections: { id: SectionId; label: string }[] = [
    { id: "general", label: "General" },
    { id: "networking", label: "Networking" },
    ...(http ? [{ id: "access" as const, label: "Access" }] : []),
    ...(app ? [{ id: "storage" as const, label: "Storage" }] : []),
    ...(http && projectName ? [{ id: "maintenance" as const, label: "Maintenance" }] : []),
    { id: "advanced", label: "Advanced" },
  ];
  const [section, setSection] = useTab(
    sections.map((s) => s.id),
    "general",
    "section",
  );

  // Unsaved forms, as "section form" keys.
  const [dirty, setDirty] = useState<Set<string>>(new Set());
  const reporters = useMemo(
    () =>
      Object.fromEntries(
        sectionIds.map((id) => [
          id,
          (form: string, on: boolean) =>
            setDirty((s) => {
              const key = `${id} ${form}`;
              if (s.has(key) === on) return s;
              const next = new Set(s);
              if (on) next.add(key);
              else next.delete(key);
              return next;
            }),
        ]),
      ) as Record<SectionId, (form: string, dirty: boolean) => void>,
    [],
  );
  const isDirty = (id: SectionId) => [...dirty].some((k) => k.startsWith(`${id} `));

  const rename = useMutation({
    meta: { error: "Couldn't rename the service" },
    mutationFn: (name: string) => api.renameService(svc.id, name),
    onSuccess: (updated) => {
      qc.setQueryData(["service", svc.id], updated);
      qc.invalidateQueries({ queryKey: ["services", updated.projectId] });
    },
  });

  // Keys reset each card's form when another service opens; siblings need distinct ones.
  const content: Record<SectionId, ReactNode> = {
    general: (
      <GitOwned path={gitPath}>
        <RenameCard
          key={`rename-${svc.name}`}
          name={svc.name}
          description={
            isDb
              ? "Nothing restarts. The apps using it get their references updated and are redeployed."
              : "Nothing restarts. Until its next deploy, other services can still reach it by its old name."
          }
          disabled={!!gitPath}
          pending={rename.isPending}
          onRename={(name) => rename.mutate(name)}
        />
        <GeneralCard key={`general-${svc.id}`} svc={svc} />
      </GitOwned>
    ),
    networking: (
      <>
        {app && (
          <GitOwned path={gitPath}>
            <DomainCard key={`domain-${svc.id}`} svc={svc} />
            <PortsCard key={`ports-${svc.id}`} svc={svc} />
          </GitOwned>
        )}
        {/* Not in the compose file, so git projects set them here too. */}
        {!svc.hostNetwork && <NetworksCard key={`networks-${svc.id}`} svc={svc} projectName={projectName ?? ""} />}
      </>
    ),
    access: (
      <GitOwned path={gitPath}>
        <AccessCard key={`access-${svc.id}`} svc={svc} />
      </GitOwned>
    ),
    storage: (
      <GitOwned path={gitPath}>
        <VolumesCard key={`volumes-${svc.id}`} svc={svc} />
      </GitOwned>
    ),
    maintenance: projectName && <MaintenanceCard key={`maintenance-${svc.id}`} svc={svc} projectName={projectName} />,
    advanced: (
      <>
        {app && (
          <GitOwned path={gitPath}>
            <HostAccessCard key={`host-${svc.id}`} svc={svc} />
          </GitOwned>
        )}
        {danger}
      </>
    ),
  };

  return (
    <Tabs value={section} onValueChange={setSection} className="gap-4">
      <TabsList className="max-w-full justify-start overflow-x-auto [scrollbar-width:none]">
        {sections.map((s) => (
          <TabsTrigger key={s.id} value={s.id} className="flex-none">
            {s.label}
            {isDirty(s.id) && <span title="Unsaved changes" className="size-1.5 rounded-full bg-amber-500" />}
          </TabsTrigger>
        ))}
      </TabsList>
      {sections.map((s) => (
        <TabsContent
          key={s.id}
          value={s.id}
          forceMount
          className="flex flex-col gap-6 data-[state=inactive]:hidden"
          // Saving a hidden form that fails validation: show it, so the browser can point at the field.
          onInvalidCapture={() => setSection(s.id)}
        >
          <DirtyContext.Provider value={reporters[s.id]}>{content[s.id]}</DirtyContext.Provider>
        </TabsContent>
      ))}
    </Tabs>
  );
}
