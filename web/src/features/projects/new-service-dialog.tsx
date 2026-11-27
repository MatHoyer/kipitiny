import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Box, ChevronLeft, Plus } from "lucide-react";
import { useState, type FormEvent, type ReactNode } from "react";
import { useNavigate } from "react-router";
import { ServiceIcon } from "@/components/service-icon";
import { ChoiceTile } from "@kipit-labs/kipitidy/choice-tile";
import { DomainField } from "@/features/services/domain-field";
import { EnvEditor } from "@/features/services/env-editor";
import { Button } from "@kipit-labs/kipitidy/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@kipit-labs/kipitidy/dialog";
import { FloatingInput } from "@kipit-labs/kipitidy/floating-input";
import { FloatingSelect } from "@kipit-labs/kipitidy/floating-select";
import { envMap, envSecrets, type EnvRow } from "@/lib/format";
import { api, isDatabase, type DatabaseKind, type DatabaseRef, type ServiceInput } from "../../api";

const emptyForm = {
  name: "",
  image: "",
  port: "",
  domain: "",
  replicas: "1",
  // Empty: the kind's default (its first version, its memory).
  version: "",
  memory: "",
};

type DatabaseChoice = {
  kind: DatabaseKind;
  title: string;
  /** Major versions offered, newest first. */
  versions: string[];
  version: string;
  image: (version: string) => string;
  memory: number;
  minMemory: number;
  memoryHint: string;
  placeholder: string;
};

// PostGIS's images are PostgreSQL's with the extension: the same kind, another image.
const postgisTags: Record<string, string> = { "18": "18-3.6", "17": "17-3.6", "16": "16-3.5", "15": "15-3.5" };

const databaseChoices: Record<DatabaseKind | "postgis", DatabaseChoice> = {
  postgres: {
    kind: "postgres",
    title: "PostgreSQL",
    versions: ["18", "17", "16", "15"],
    version: "17",
    image: (v) => `postgres:${v}-alpine`,
    memory: 512,
    minMemory: 128,
    memoryHint: "Container limit; shared_buffers is sized from it.",
    placeholder: "db",
  },
  postgis: {
    kind: "postgres",
    title: "PostGIS",
    versions: ["18", "17", "16", "15"],
    version: "17",
    // Alpine like postgres's own: an existing database can switch to it (same collations).
    image: (v) => `postgis/postgis:${postgisTags[v]}-alpine`,
    memory: 512,
    minMemory: 128,
    memoryHint: "Container limit; shared_buffers is sized from it.",
    placeholder: "db",
  },
  redis: {
    kind: "redis",
    title: "Redis",
    versions: ["8", "7"],
    version: "8",
    image: (v) => `redis:${v}-alpine`,
    memory: 256,
    minMemory: 32,
    memoryHint: "Container limit; Redis keeps its data within 75% of it.",
    placeholder: "cache",
  },
  mysql: {
    kind: "mysql",
    title: "MySQL",
    versions: ["9", "8.4"],
    version: "8.4",
    image: (v) => `mysql:${v}`,
    memory: 512,
    minMemory: 256,
    memoryHint: "Container limit; half of it goes to InnoDB's buffer pool.",
    placeholder: "mysql",
  },
  mariadb: {
    kind: "mariadb",
    title: "MariaDB",
    versions: ["11.8", "11.4", "10.11"],
    version: "11.8",
    image: (v) => `mariadb:${v}`,
    memory: 512,
    minMemory: 256,
    memoryHint: "Container limit; half of it goes to InnoDB's buffer pool.",
    placeholder: "mariadb",
  },
  mongodb: {
    kind: "mongodb",
    title: "MongoDB",
    // 8.0 refuses to start on Linux 6.19 and later.
    versions: ["8.2", "7.0"],
    version: "8.2",
    image: (v) => `mongo:${v}`,
    memory: 1024,
    minMemory: 512,
    memoryHint: "Container limit; WiredTiger's cache is sized from it.",
    placeholder: "mongo",
  },
};

type Choice = "image" | keyof typeof databaseChoices;

const choiceGroups: { label: string; choices: { id: Choice; icon: ReactNode; title: string; description: string }[] }[] = [
  {
    label: "Apps",
    choices: [
      { id: "image", icon: <Box />, title: "Docker image", description: "Run a published image." },
    ],
  },
  {
    label: "Databases",
    choices: [
      ...dbChoice("postgres", "Relational database, backed up on a schedule."),
      ...dbChoice("postgis", "PostgreSQL with geographic types and queries."),
      ...dbChoice("mysql", "The most common relational database for web apps."),
      ...dbChoice("mariadb", "MySQL-compatible relational database."),
      ...dbChoice("mongodb", "Document database for JSON-shaped data."),
      ...dbChoice("redis", "In-memory store for caches, queues and sessions."),
    ],
  },
];

function dbChoice(id: keyof typeof databaseChoices, description: string) {
  const { kind, title, image, version } = databaseChoices[id];
  return [{ id, icon: <ServiceIcon service={{ kind, image: image(version) }} />, title, description }];
}

const choiceTitle = (c: Choice) => (c === "image" ? "New app from a Docker image" : `New ${databaseChoices[c].title} database`);

/** Creates a service in a project. stay keeps the user where they are (the map) instead of opening it. */
export function NewServiceDialog({ projectId, trigger, stay }: { projectId: string; trigger?: ReactNode; stay?: boolean }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const services = useQuery({ queryKey: ["services", projectId], queryFn: () => api.services(projectId) });
  const databases: DatabaseRef[] = services.data?.flatMap((s) => (isDatabase(s.kind) ? [{ name: s.name, kind: s.kind }] : [])) ?? [];
  const project = useQuery({ queryKey: ["project", projectId], queryFn: () => api.project(projectId) });

  const [open, setOpen] = useState(false);
  const [env, setEnv] = useState<EnvRow[]>([]);
  // First step: what to create; the form follows.
  const [choice, setChoice] = useState<Choice | null>(null);
  const db = !choice || choice === "image" ? null : databaseChoices[choice];
  const kind = db?.kind ?? "app";
  const [form, setForm] = useState(emptyForm);
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });

  const create = useMutation({
    meta: { error: "Couldn't create the service" },
    mutationFn: async (deploy: boolean) => {
      const input: ServiceInput =
        db
          ? {
              kind,
              name: form.name.trim(),
              image: db.image(form.version || db.version),
              memoryMb: Number(form.memory) || db.memory,
            }
          : {
              kind,
              name: form.name.trim(),
              image: form.image.trim(),
              port: Number(form.port) || 0,
              domain: form.domain.trim(),
              replicas: Number(form.replicas) || 1,
              env: envMap(env),
              secrets: envSecrets(env),
            };
      const svc = await api.createService(projectId, input);
      if (deploy) await api.deploy(svc.id);
      return svc;
    },
    onSuccess: (svc) => {
      qc.invalidateQueries({ queryKey: ["services", projectId] });
      qc.invalidateQueries({ queryKey: ["topology"] });
      close();
      if (!stay) navigate(`/services/${svc.id}`);
    },
  });

  const close = () => {
    setOpen(false);
    setForm(emptyForm);
    setEnv([]);
    setChoice(null);
    create.reset();
  };
  const onOpenChange = (next: boolean) => {
    if (create.isPending) return;
    if (!next) return close();
    setOpen(true);
  };
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    create.mutate(true);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        {trigger ?? (
          <Button size="sm">
            <Plus data-icon="inline-start" />
            New service
          </Button>
        )}
      </DialogTrigger>
      <DialogContent className="sm:max-w-2xl">
        {!choice ? (
          <>
            <DialogHeader>
              <DialogTitle>New service</DialogTitle>
              <DialogDescription>What do you want to run?</DialogDescription>
            </DialogHeader>
            {choiceGroups.map((g) => (
              <div key={g.label} className="space-y-2">
                <h3 className="text-xs font-medium tracking-wide text-muted-foreground uppercase">{g.label}</h3>
                <div className="grid gap-3 sm:grid-cols-3">
                  {g.choices.map((c) => (
                    <ChoiceTile key={c.id} icon={c.icon} title={c.title} description={c.description} onClick={() => setChoice(c.id)} />
                  ))}
                </div>
              </div>
            ))}
          </>
        ) : (
          <form onSubmit={onSubmit} className="contents">
            <DialogHeader>
              <DialogTitle>{choiceTitle(choice)}</DialogTitle>
              <DialogDescription>
                {kind === "app"
                  ? "Create it to set volumes, a pre-deploy command or health checks before its first deploy."
                  : "It is deployed as soon as it is created."}
              </DialogDescription>
            </DialogHeader>
            <div className="grid gap-4 sm:grid-cols-2">
              <FloatingInput
                label="Name"
                required
                autoFocus
                value={form.name}
                onChange={set("name")}
                placeholder={db ? db.placeholder : "web"}
                description={kind !== "app" ? "Also the hostname apps use to connect." : undefined}
              />
              {db ? (
                <>
                  <FloatingSelect
                    key={`version-${choice}`}
                    label="Version"
                    value={form.version || db.version}
                    onValueChange={(v) => setForm({ ...form, version: v })}
                    options={db.versions.map((v) => ({ value: v, label: `${db.title} ${v}` }))}
                  />
                  <FloatingInput
                    label="Memory (MB)"
                    type="number"
                    min={db.minMemory}
                    step={db.minMemory < 128 ? 64 : 128}
                    value={form.memory}
                    onChange={set("memory")}
                    placeholder={String(db.memory)}
                    description={db.memoryHint}
                  />
                </>
              ) : (
                <>
                  <FloatingInput
                    label="Image"
                    required
                    value={form.image}
                    onChange={set("image")}
                    placeholder="ghcr.io/org/app:latest"
                    className="sm:col-span-2"
                  />
                  <DomainField value={form.domain} onChange={(domain) => setForm({ ...form, domain })} className="sm:col-span-2" />
                  <FloatingInput
                    label="Container port"
                    type="number"
                    min={1}
                    max={65535}
                    value={form.port}
                    onChange={set("port")}
                    placeholder="3000"
                    description="The port your app listens on. Required with a domain."
                  />
                  <FloatingInput label="Replicas" type="number" min={1} max={10} value={form.replicas} onChange={set("replicas")} />
                  <EnvEditor
                    rows={env}
                    onChange={setEnv}
                    vars={Object.keys(project.data?.env ?? {}).sort()}
                    secretVars={project.data?.secrets}
                    databases={databases}
                    className="sm:col-span-2"
                  />
                </>
              )}
            </div>
            <DialogFooter>
              <Button type="button" variant="ghost" disabled={create.isPending} onClick={() => setChoice(null)}>
                <ChevronLeft data-icon="inline-start" />
                Back
              </Button>
              {kind === "app" && (
                <Button
                  type="button"
                  variant="outline"
                  disabled={create.isPending}
                  loading={create.isPending && !create.variables}
                  onClick={(e) => e.currentTarget.form?.reportValidity() && create.mutate(false)}
                >
                  Create
                </Button>
              )}
              <Button type="submit" disabled={create.isPending} loading={create.isPending && create.variables}>
                Create & deploy
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
