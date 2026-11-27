import { ChevronLeft } from "lucide-react";
import { useState, type FormEvent } from "react";
import { databaseLabels } from "@/components/brand-icons";
import { ServiceIcon } from "@/components/service-icon";
import { TemplateValue } from "@/features/projects/template-value";
import { Button } from "@kipit-labs/kipitidy/button";
import { DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@kipit-labs/kipitidy/dialog";
import { FloatingSelect } from "@kipit-labs/kipitidy/floating-select";
import { Segmented, SegmentedItem } from "@kipit-labs/kipitidy/segmented";
import { dbFields, dbRef, type EnvRow } from "@/lib/format";
import { type DatabaseKind, type DatabaseRef } from "../../api";

type DbShape = { label: string; fields: string[][] };

/** Laravel's and most PHP apps' names. */
const mysqlShapes = {
  url: { label: "DATABASE_URL", fields: [["DATABASE_URL", "URL"]] },
  split: {
    label: "DB_HOST, DB_PORT, DB_DATABASE…",
    fields: [
      ["DB_HOST", "HOST"],
      ["DB_PORT", "PORT"],
      ["DB_DATABASE", "DATABASE"],
      ["DB_USERNAME", "USER"],
      ["DB_PASSWORD", "PASSWORD"],
    ],
  },
};

/** The two ways to connect a database, as the kind's clients expect them. */
const dbShapes: Record<DatabaseKind, { url: DbShape; split: DbShape }> = {
  postgres: {
    url: { label: "DATABASE_URL", fields: [["DATABASE_URL", "URL"]] },
    split: {
      label: "PGHOST, PGPORT, PGUSER…",
      fields: dbFields.postgres.filter((f) => f !== "URL").map((f) => [`PG${f}`, f]),
    },
  },
  redis: {
    url: { label: "REDIS_URL", fields: [["REDIS_URL", "URL"]] },
    split: {
      label: "REDIS_HOST, REDIS_PORT…",
      fields: ["HOST", "PORT", "PASSWORD"].map((f) => [`REDIS_${f}`, f]),
    },
  },
  mysql: mysqlShapes,
  mariadb: mysqlShapes,
  mongodb: {
    url: { label: "MONGODB_URI", fields: [["MONGODB_URI", "URL"]] },
    split: {
      label: "MONGO_HOST, MONGO_PORT…",
      fields: dbFields.mongodb.filter((f) => f !== "URL").map((f) => [`MONGO_${f}`, f]),
    },
  },
};

/** Adds a database's credentials as secrets referencing it. */
export function DatabaseForm({
  databases,
  taken,
  onBack,
  onDone,
}: {
  databases: DatabaseRef[];
  taken: string[];
  onBack: () => void;
  onDone: (rows: EnvRow[]) => void;
}) {
  const [name, setName] = useState(databases[0].name);
  const db = databases.find((d) => d.name === name) ?? databases[0];
  const shapes = dbShapes[db.kind];
  const [shape, setShape] = useState<"url" | "split">("url");
  // The usual name, or prefixed by the database when taken (several
  // databases, one service).
  const entryName = (key: string) => (taken.includes(key) ? `${db.name.toUpperCase().replaceAll("-", "_")}_${key}` : key);
  const rows = shapes[shape].fields.map(([key, field]) => ({
    key: entryName(key),
    value: dbRef(db.name, field),
    secret: true,
  }));
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    e.stopPropagation();
    onDone(rows);
  };

  return (
    <form onSubmit={onSubmit} className="contents">
      <DialogHeader>
        <DialogTitle className="flex items-center gap-2 [&>svg]:size-5">
          <ServiceIcon service={db} />
          Connect a database
        </DialogTitle>
        <DialogDescription>Added as secrets that follow the database&apos;s credentials.</DialogDescription>
      </DialogHeader>
      <div className="space-y-4">
        {databases.length > 1 && (
          <FloatingSelect
            label="Database"
            value={db.name}
            onValueChange={setName}
            options={databases.map((d) => ({ value: d.name, label: `${d.name} (${databaseLabels[d.kind]})` }))}
          />
        )}
        <Segmented
          value={shape}
          onValueChange={(v) => setShape(v as "url" | "split")}
          aria-label="Variables"
          className="w-full"
        >
          {Object.entries(shapes).map(([k, s]) => (
            <SegmentedItem key={k} value={k} className="flex-1 font-mono text-xs">
              {s.label}
            </SegmentedItem>
          ))}
        </Segmented>
        <ul className="space-y-1 rounded-xl border p-3">
          {rows.map((r) => (
            <li key={r.key} className="flex items-center gap-3 text-sm">
              <span className="w-2/5 shrink-0 truncate font-mono">{r.key}</span>
              <TemplateValue value={r.value} />
            </li>
          ))}
        </ul>
      </div>
      <DialogFooter>
        <Button type="button" variant="ghost" onClick={onBack}>
          <ChevronLeft data-icon="inline-start" />
          Back
        </Button>
        <Button type="submit">Connect</Button>
      </DialogFooter>
    </form>
  );
}
