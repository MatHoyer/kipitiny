import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useId, useState, type FormEvent } from "react";
import { toast } from "sonner";
import { Mono } from "@/components/common";
import { Section } from "@kipit-labs/kipitidy/section";
import { EnvEditor } from "@/features/services/env-editor";
import { SaveBar } from "@/components/save-bar";
import { envMap, envRows, envSecrets, sameEnv, type EnvRow } from "@/lib/format";
import { api, isDatabase, type Service as ServiceT } from "../../api";

export function EnvironmentCard({ svc, gitManaged, canEdit }: { svc: ServiceT; gitManaged: boolean; canEdit: boolean }) {
  const qc = useQueryClient();
  // A database's env is generated at creation: shown, never edited. A git
  // project's env is its compose file's.
  const isDb = isDatabase(svc.kind);
  const readOnly = isDb || gitManaged || !canEdit;
  const project = useQuery({ queryKey: ["project", svc.projectId], queryFn: () => api.project(svc.projectId), enabled: !isDb });
  const siblings = useQuery({ queryKey: ["services", svc.projectId], queryFn: () => api.services(svc.projectId), enabled: !isDb });
  const databases = siblings.data?.flatMap((s) => (isDatabase(s.kind) ? [{ name: s.name, kind: s.kind }] : []));
  const [rows, setRows] = useState<EnvRow[]>(() => envRows(svc.env, svc.secrets));
  const dirty = !sameEnv(rows, svc.env, svc.secrets);
  const formId = useId();
  const save = useMutation({
    meta: { error: "Couldn't save the environment" },
    mutationFn: () => api.updateService(svc.id, { env: envMap(rows), secrets: envSecrets(rows) }),
    onSuccess: (updated) => {
      qc.setQueryData(["service", svc.id], updated);
      setRows(envRows(updated.env, updated.secrets));
      toast.success("Environment saved", { description: "Deploy to apply." });
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <Section
      plain
      description={
        isDb ? (
          "Generated at creation and shared with the apps that connect, so it can't be changed."
        ) : (
          <>
            Use the project&apos;s shared entries with <Mono>{"{{ project.NAME }}"}</Mono>, its databases with{" "}
            <Mono>{"{{ db.NAME.URL }}"}</Mono> and password manager secrets with{" "}
            <Mono>{"{{ pass://Vault/Item/field }}"}</Mono> (Integrations › Password managers). Changes apply on the next
            deploy.
          </>
        )
      }
    >
      <form id={formId} onSubmit={onSubmit} className="space-y-4">
        <EnvEditor
          showLabel={false}
          rows={rows}
          onChange={setRows}
          vars={Object.keys(project.data?.env ?? {}).sort()}
          secretVars={project.data?.secrets}
          databases={databases}
          passwordManagers
          readOnly={readOnly}
        />
      </form>
      {!readOnly && <SaveBar form={formId} dirty={dirty} saving={save.isPending} onReset={() => setRows(envRows(svc.env, svc.secrets))} />}
    </Section>
  );
}
