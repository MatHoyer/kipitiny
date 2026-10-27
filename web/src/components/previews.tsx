import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ExternalLink, GitBranch, GitPullRequestArrow, Trash2 } from "lucide-react";
import { useId, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { toast } from "sonner";
import { CheckboxField, EmptyState, ErrorText, Loading, Mono, Section, Tag } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DomainField } from "@/components/domain-field";
import { EnvEditor } from "@/components/env-editor";
import { ServiceStates } from "@/components/service-states";
import { memoryOf, useUsage } from "@/components/usage";
import { PreviewSeedCard } from "@/components/preview-seed";
import { SaveBar } from "@/components/save-bar";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingSelect } from "@/components/ui/floating-select";
import { envMap, envRows, envSecrets, formatBytes, sameEnv, timeAgo, type EnvRow } from "@/lib/format";
import { api, type PreviewSettings, type PreviewView, type Project, type SeedMode } from "../api";

const DAY = 86_400;

/** A project's preview environments, and how they differ from it. */
export function Previews({ project, canDeploy, canEdit }: { project: Project; canDeploy: boolean; canEdit: boolean }) {
  return (
    <>
      <PreviewList project={project} canDeploy={canDeploy} />
      <PreviewSettingsCard key={project.id} project={project} canEdit={canEdit} />
      <AnonymizedSeed project={project} canEdit={canEdit} />
    </>
  );
}

function PreviewList({ project, canDeploy }: { project: Project; canDeploy: boolean }) {
  const previews = useQuery({
    queryKey: ["previews", project.id],
    queryFn: () => api.previews(project.id),
    refetchInterval: 15_000,
  });
  return (
    <Section
      title="Preview environments"
      description={
        <>
          Copies of the project that CI opens per branch or stack of pull requests, with{" "}
          <Mono>kipitiny preview --project {project.name} --name pr-12 --tag web=sha-abc</Mono>.{" "}
          <a href="/docs/previews" className="underline underline-offset-2">
            How to set it up
          </a>
          .
        </>
      }
    >
      {previews.error ? (
        <ErrorText error={previews.error} />
      ) : !previews.data ? (
        <Loading />
      ) : previews.data.length === 0 ? (
        <EmptyState
          icon={GitPullRequestArrow}
          title="No previews"
          description="Set the base domain below, then deploy one from CI."
        />
      ) : (
        <div className="grid gap-3 sm:grid-cols-2">
          {previews.data.map((p) => (
            <PreviewCard key={p.id} parent={project} preview={p} canDeploy={canDeploy} />
          ))}
        </div>
      )}
    </Section>
  );
}

function PreviewCard({ parent, preview: p, canDeploy }: { parent: Project; preview: PreviewView; canDeploy: boolean }) {
  const qc = useQueryClient();
  const info = p.preview ?? {};
  const name = info.name ?? p.name;
  const close = useMutation({
    meta: { error: "Couldn't close the preview" },
    mutationFn: () => api.closePreview(parent.id, name),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["previews", parent.id] });
      qc.invalidateQueries({ queryKey: ["projects"] });
      toast.success(`Preview ${name} closed`);
    },
  });
  const urls = Object.entries(p.urls).sort(([a], [b]) => a.localeCompare(b));
  // Same key as the project page and the projects list, so the cache is shared.
  const services = useQuery({ queryKey: ["services", p.id], queryFn: () => api.services(p.id), refetchInterval: 15_000 });
  const memory = memoryOf(useUsage().data, (u) => u.projectId === p.id);
  return (
    // The name's link covers the card; the URLs and the close button sit above it.
    <Card className="relative gap-2 px-4 transition-all has-[a[data-cover]:hover]:-translate-y-px has-[a[data-cover]:hover]:shadow-md">
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <Link to={`/projects/${p.id}`} data-cover className="font-medium outline-none after:absolute after:inset-0 after:rounded-xl after:content-[''] hover:underline focus-visible:after:ring-3 focus-visible:after:ring-ring/50">
            {name}
          </Link>
          <p className="flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
            {info.branch && (
              <span className="inline-flex min-w-0 items-center gap-1">
                <GitBranch className="size-3 shrink-0" />
                <span className="truncate font-mono">{info.branch}</span>
              </span>
            )}
            {info.commit && <span className="font-mono">{info.commit.slice(0, 7)}</span>}
            {info.deployedAt && <span>deployed {timeAgo(info.deployedAt)}</span>}
            {!!memory && <span>{formatBytes(memory)} memory</span>}
          </p>
        </div>
        {canDeploy && (
          <ConfirmDialog
            trigger={
              <Button variant="ghost" size="icon-sm" className="relative z-10" aria-label={`Close preview ${name}`} disabled={close.isPending}>
                <Trash2 />
              </Button>
            }
            title={`Close preview ${name}?`}
            description="Its containers, databases and volumes are deleted. CI opens it again on its next deploy."
            onConfirm={() => close.mutate()}
          />
        )}
      </div>
      {services.data && <ServiceStates services={services.data} />}
      {urls.length > 0 && (
        <ul className="relative z-10 w-fit max-w-full space-y-0.5">
          {urls.map(([svc, url]) => (
            <li key={svc} className="flex min-w-0 items-center gap-1.5 text-xs">
              <span className="shrink-0 text-muted-foreground">{svc}</span>
              <a href={url} target="_blank" rel="noreferrer" className="inline-flex min-w-0 items-center gap-1 font-mono hover:underline">
                <span className="truncate">{url.replace(/^https:\/\//, "")}</span>
                <ExternalLink className="size-3 shrink-0" />
              </a>
            </li>
          ))}
        </ul>
      )}
      {!!info.ttlSeconds && <Tag className="w-fit">closes after {Math.round(info.ttlSeconds / DAY)} days unused</Tag>}
    </Card>
  );
}

const baseDomainNote = (example: string) => (
  <>
    Apps answer at <Mono>{`pr-12-web.${example}`}</Mono> (<Mono>{`pr-12.${example}`}</Mono> with one app): point{" "}
    <Mono>{`*.${example}`}</Mono> at the server. A domain of its own keeps production's cookies away.
  </>
);

const seedNotes: Record<SeedMode, string> = {
  "": "Previews start with empty databases.",
  anonymized:
    "CI may pass --seed to fill a new preview's databases from the latest backups, anonymized with each database's rules (below once saved).",
  raw: "CI may pass --seed to fill a new preview's databases from the latest backups. That copies production data, personal data included, into the preview.",
};

/** The rules of each database, when previews are seeded with anonymized data. */
function AnonymizedSeed({ project, canEdit }: { project: Project; canEdit: boolean }) {
  const settings = useQuery({ queryKey: ["preview-settings", project.id], queryFn: () => api.previewSettings(project.id) });
  return settings.data?.seed === "anonymized" ? <PreviewSeedCard project={project} canEdit={canEdit} /> : null;
}

/** The settings form's fields, as saved. */
const settingsForm = (s: PreviewSettings) => ({
  baseDomain: s.baseDomain,
  ttlDays: s.ttlSeconds ? String(s.ttlSeconds / DAY) : "",
  sleepMinutes: s.sleepAfterSeconds ? String(s.sleepAfterSeconds / 60) : "",
  inherit: [...s.inherit].sort(),
  seed: s.seed,
});

function PreviewSettingsCard({ project, canEdit }: { project: Project; canEdit: boolean }) {
  const settings = useQuery({ queryKey: ["preview-settings", project.id], queryFn: () => api.previewSettings(project.id) });
  if (settings.error) return <ErrorText error={settings.error} />;
  if (!settings.data) return <Loading />;
  return <PreviewSettingsForm key={settings.data.baseDomain} project={project} saved={settings.data} canEdit={canEdit} />;
}

function PreviewSettingsForm({ project, saved, canEdit }: { project: Project; saved: PreviewSettings; canEdit: boolean }) {
  const qc = useQueryClient();
  const [form, setForm] = useState(() => settingsForm(saved));
  const [rows, setRows] = useState<EnvRow[]>(() => envRows(saved.env, saved.secrets));
  const dirty = JSON.stringify(form) !== JSON.stringify(settingsForm(saved)) || !sameEnv(rows, saved.env, saved.secrets);
  const formId = useId();
  const save = useMutation({
    meta: { error: "Couldn't save the preview settings" },
    mutationFn: () =>
      api.setPreviewSettings(project.id, {
        baseDomain: form.baseDomain.trim(),
        env: envMap(rows),
        secrets: envSecrets(rows),
        inherit: form.inherit,
        ttlSeconds: Math.round((Number(form.ttlDays) || 0) * DAY),
        sleepAfterSeconds: Math.round((Number(form.sleepMinutes) || 0) * 60),
        seed: form.seed,
      }),
    onSuccess: (updated) => {
      qc.setQueryData(["preview-settings", project.id], updated);
      setForm(settingsForm(updated));
      setRows(envRows(updated.env, updated.secrets));
      toast.success("Preview settings saved", { description: "Each preview gets them on its next deploy." });
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };
  const toggleInherit = (name: string, on: boolean) =>
    setForm({ ...form, inherit: on ? [...form.inherit, name].sort() : form.inherit.filter((n) => n !== name) });
  const example = form.baseDomain.trim() || "preview.example.com";

  return (
    <Section
      title="Preview settings"
      description="What previews get instead of production: their own domain and values. Production's secrets never reach a preview unless listed under Inherit."
    >
      <form id={formId} onSubmit={onSubmit} className="grid gap-4 sm:grid-cols-3">
        {canEdit ? (
          <DomainField
            label="Base domain"
            emptyLabel="Choose a domain…"
            emptyText="Previews need a base domain."
            value={form.baseDomain}
            onChange={(baseDomain) => setForm({ ...form, baseDomain })}
            host={(d) => `pr-12-web.${d}`}
            className="col-span-full"
            note={baseDomainNote(example)}
          />
        ) : (
          <FloatingInput label="Base domain" value={form.baseDomain} disabled className="col-span-full" description={baseDomainNote(example)} />
        )}
        <FloatingInput
          label="Close after unused (days)"
          type="number"
          min={0}
          value={form.ttlDays}
          onChange={(e) => setForm({ ...form, ttlDays: e.target.value })}
          placeholder="7"
          disabled={!canEdit}
          description="No request and no deploy for that long. Empty: 7 days."
        />
        <FloatingInput
          label="Sleep after idle (min)"
          type="number"
          min={5}
          value={form.sleepMinutes}
          onChange={(e) => setForm({ ...form, sleepMinutes: e.target.value })}
          placeholder="30"
          className="sm:col-span-2"
          disabled={!canEdit}
          description="Previews' apps stop when unused and start on the next request. Empty: 30 minutes."
        />
        <div className="col-span-full space-y-2">
          <EnvEditor
            label="Variables & secrets"
            rows={rows}
            onChange={setRows}
            passwordManagers
            readOnly={!canEdit}
            description={
              <>
                Project variables of every preview, over the project's: a service's secret <Mono>API_KEY</Mono> needs one named{" "}
                <Mono>API_KEY</Mono> here. Production URLs are refused; write them as <Mono>{"{{ app.web.URL }}"}</Mono>.
              </>
            }
          />
        </div>
        {project.secrets.length > 0 && (
          <div className="col-span-full space-y-2">
            <p className="text-sm font-medium">Inherit from the project</p>
            <p className="text-xs text-muted-foreground">Secrets previews may use as they are, e.g. an error tracker's DSN.</p>
            <div className="grid gap-2 sm:grid-cols-3">
              {project.secrets.map((name) => (
                <CheckboxField
                  key={name}
                  label={<span className="font-mono">{name}</span>}
                  checked={form.inherit.includes(name)}
                  onCheckedChange={(on) => canEdit && toggleInherit(name, on)}
                />
              ))}
            </div>
          </div>
        )}
        <FloatingSelect
          label="Seed new previews with"
          className="col-span-full"
          value={form.seed || "off"}
          onValueChange={(v) => setForm({ ...form, seed: (v === "off" ? "" : v) as SeedMode })}
          options={[
            { value: "off", label: "Nothing: --seed is refused" },
            { value: "anonymized", label: "Anonymized data" },
            { value: "raw", label: "Production data as is" },
          ]}
          disabled={!canEdit}
          description={seedNotes[form.seed]}
        />
      </form>
      {canEdit && (
        <SaveBar
          form={formId}
          dirty={dirty}
          saving={save.isPending}
          onReset={() => {
            setForm(settingsForm(saved));
            setRows(envRows(saved.env, saved.secrets));
          }}
        />
      )}
    </Section>
  );
}
