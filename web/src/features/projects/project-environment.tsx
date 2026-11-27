import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useId, useState, type FormEvent } from "react";
import { toast } from "sonner";
import { ServiceIcon } from "@/components/service-icon";
import { Mono } from "@/components/common";
import { CopyButton } from "@kipit-labs/kipitidy/copy-button";
import { Section } from "@kipit-labs/kipitidy/section";
import { EnvEditor } from "@/features/services/env-editor";
import { SaveBar } from "@/components/save-bar";
import { dbFields, dbRef, envMap, envRows, envSecrets, sameEnv, type EnvRow } from "@/lib/format";
import { api, type DatabaseRef, type Project as ProjectT } from "../../api";

export function SharedVariables({ project, canEdit }: { project: ProjectT; canEdit: boolean }) {
  const qc = useQueryClient();
  const [rows, setRows] = useState<EnvRow[]>(() => envRows(project.env, project.secrets));
  const dirty = !sameEnv(rows, project.env, project.secrets);
  const formId = useId();
  const save = useMutation({
    meta: { error: "Couldn't save the shared variables" },
    mutationFn: () => api.setProjectEnv(project.id, envMap(rows), envSecrets(rows)),
    onSuccess: (updated) => {
      qc.setQueryData(["project", project.id], updated);
      setRows(envRows(updated.env, updated.secrets));
      toast.success("Shared variables saved", { description: "Deploy the services that use them to apply." });
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <Section
      title="Shared variables & secrets"
      description={
        <>
          Available to every service of the project as <Mono>{"{{ project.NAME }}"}</Mono>. Values can be password
          manager secrets, e.g. <Mono>{"{{ pass://Vault/Item/field }}"}</Mono>.
        </>
      }
    >
      <form id={formId} onSubmit={onSubmit} className="space-y-4">
        <EnvEditor
          showLabel={false}
          rows={rows}
          onChange={setRows}
          passwordManagers
          description="An entry used by a service can't be removed."
          readOnly={!canEdit}
        />
      </form>
      {canEdit && (
        <SaveBar form={formId} dirty={dirty} saving={save.isPending} onReset={() => setRows(envRows(project.env, project.secrets))} />
      )}
    </Section>
  );
}

/** The credentials each database offers to the services' env. */
export function DatabaseReferences({ dbs }: { dbs: DatabaseRef[] }) {
  if (dbs.length === 0) return null;
  return (
    <Section
      title="Databases"
      description="Credentials a service can use in its environment, e.g. under Secrets → Connect database."
    >
      {dbs.map((db) => (
        <div key={db.name} className="space-y-1.5">
          <p className="flex items-center gap-2 text-sm font-medium">
            <ServiceIcon service={db} className="size-4" />
            {db.name}
          </p>
          <ul className="flex flex-wrap gap-1.5">
            {dbFields[db.kind].map((f) => (
              <li key={f} className="inline-flex items-center gap-0.5 rounded-md bg-muted py-0.5 pr-0.5 pl-2 font-mono text-xs">
                {dbRef(db.name, f)}
                <CopyButton value={dbRef(db.name, f)} label={`Copy ${f} reference`} />
              </li>
            ))}
          </ul>
        </div>
      ))}
    </Section>
  );
}
