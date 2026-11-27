import type { MongoDocs, Network, RowChanges, DocChanges, KeyChanges, NewColumn, ChangeResult, PgTable, RedisKeys, RedisValue } from "@/api";
import { type DemoDb } from "./seed";
import { demoOid, docChanges, glob, keyChanges, pgConsole, redisConsole, rows, tableChanges } from "./databrowser";
import type { Route } from "./ops";
import { db, invalid, nameRe, newId, notFound, now, projectOf, save, serviceOf } from "./state";

export const routesD: Route[] = [
  [
    "GET",
    /^\/services\/([^/]+)\/data\/tables$/,
    ({ m }): PgTable[] => {
      // The same demo data in every kind: MySQL's schema is its database, SQLite's main, MongoDB's tables are collections.
      const kind = serviceOf(m[1]).kind;
      const schemaOf = (t: DemoDb["tables"][number]) => (kind === "postgres" ? t.schema : kind === "app" ? "main" : "app");
      return db.tables.map((t) => ({
        schema: schemaOf(t),
        name: t.name,
        kind: kind === "mongodb" ? "collection" : "table",
        rowEstimate: t.rows.length,
        bytes: t.rows.length * 96 + 8192,
        columns:
          kind === "mongodb"
            ? []
            : t.columns.map((c) => ({
                name: c.name,
                type: c.type,
                nullable: !!c.nullable,
                primaryKey: !!c.primaryKey,
                references: c.references && { schema: schemaOf(t), ...c.references },
              })),
      }));
    },
  ],
  [
    "GET",
    /^\/services\/([^/]+)\/data\/tables\/([^/]+)\/([^/]+)$/,
    ({ m, q }) => {
      const postgres = serviceOf(m[1]).kind === "postgres";
      const t = db.tables.find((x) => (!postgres || x.schema === decodeURIComponent(m[2])) && x.name === decodeURIComponent(m[3]));
      if (!t) throw notFound();
      return rows(t, q);
    },
  ],
  [
    "POST",
    /^\/services\/([^/]+)\/data\/tables\/([^/]+)\/([^/]+)\/changes$/,
    ({ m, body }): ChangeResult => {
      const postgres = serviceOf(m[1]).kind === "postgres";
      const t = db.tables.find((x) => (!postgres || x.schema === decodeURIComponent(m[2])) && x.name === decodeURIComponent(m[3]));
      if (!t) throw notFound();
      return tableChanges(t, body.changes as RowChanges, !!body.apply, postgres);
    },
  ],
  [
    "GET",
    /^\/services\/([^/]+)\/data\/collections\/([^/]+)\/documents$/,
    ({ m, q }): MongoDocs => {
      const t = db.tables.find((x) => x.name === decodeURIComponent(m[2]));
      if (!t) throw notFound();
      // A filter on one field's value is all the demo understands.
      let filter: Record<string, unknown> = {};
      try {
        filter = JSON.parse(q.get("filter") || "{}");
      } catch {
        throw invalid("the filter is a JSON object, like {\"field\": 1}");
      }
      const docs = t.rows
        .map((r, i) => ({ _id: demoOid(i), ...Object.fromEntries(t.columns.map((c, j) => [c.name, r[j]])) }))
        .filter((d) => Object.entries(filter).every(([k, v]) => String((d as Record<string, unknown>)[k]) === String(v)));
      const skip = Number(q.get("skip")) || 0;
      const limit = Number(q.get("limit")) || 25;
      return { documents: docs.slice(skip, skip + limit).map((d) => JSON.stringify(d)), truncated: [], hasMore: docs.length > skip + limit };
    },
  ],
  [
    "POST",
    /^\/services\/([^/]+)\/data\/tables$/,
    ({ m, body }): ChangeResult => {
      const postgres = serviceOf(m[1]).kind === "postgres";
      const name = String(body.name ?? "").trim();
      const schema = postgres ? String(body.schema || "public") : "app";
      const columns = (body.columns ?? []) as NewColumn[];
      if (!name || !columns.length) throw invalid("a table needs a name and at least one column");
      if (db.tables.some((t) => t.name === name && (!postgres || t.schema === schema))) throw invalid(`relation "${name}" already exists`);
      const q = (x: string) => (postgres ? `"${x}"` : `\`${x}\``);
      const pk = columns.filter((c) => c.primaryKey).map((c) => q(c.name));
      const lines = columns.map((c) => `  ${q(c.name)} ${c.type}${!c.nullable && !c.primaryKey ? " NOT NULL" : ""}${c.default ? ` DEFAULT ${c.default}` : ""}`);
      if (pk.length) lines.push(`  PRIMARY KEY (${pk.join(", ")})`);
      const statement = `CREATE TABLE ${postgres ? `${q(schema)}.${q(name)}` : q(name)} (\n${lines.join(",\n")}\n);`;
      if (body.apply) {
        db.tables.push({ schema, name, columns: columns.map((c) => ({ name: c.name, type: c.type, primaryKey: c.primaryKey, nullable: c.nullable })), rows: [] });
        save();
      }
      return { statements: [statement], applied: !!body.apply };
    },
  ],
  [
    "POST",
    /^\/services\/([^/]+)\/data\/tables\/([^/]+)\/([^/]+)\/drop$/,
    ({ m, body }): ChangeResult => {
      const postgres = serviceOf(m[1]).kind === "postgres";
      const t = db.tables.find((x) => (!postgres || x.schema === decodeURIComponent(m[2])) && x.name === decodeURIComponent(m[3]));
      if (!t) throw notFound();
      if (body.apply) {
        db.tables = db.tables.filter((x) => x !== t);
        save();
      }
      return { statements: [postgres ? `DROP TABLE "${t.schema}"."${t.name}";` : `DROP TABLE \`${t.name}\`;`], applied: !!body.apply };
    },
  ],
  [
    "POST",
    /^\/services\/([^/]+)\/data\/collections$/,
    ({ body }): ChangeResult => {
      const name = String(body.name ?? "").trim();
      if (!/^[A-Za-z_][A-Za-z0-9_.-]{0,119}$/.test(name)) throw invalid("a collection name has letters, digits, _ . and -, starting with a letter or _");
      if (db.tables.some((t) => t.name === name)) throw invalid(`app already has a collection ${name}`);
      if (body.apply) {
        db.tables.push({ schema: "public", name, columns: [], rows: [] });
        save();
      }
      return { statements: [`db.createCollection(${JSON.stringify(name)})`], applied: !!body.apply };
    },
  ],
  [
    "POST",
    /^\/services\/([^/]+)\/data\/collections\/([^/]+)\/drop$/,
    ({ m, body }): ChangeResult => {
      const t = db.tables.find((x) => x.name === decodeURIComponent(m[2]));
      if (!t) throw notFound();
      if (body.apply) {
        db.tables = db.tables.filter((x) => x !== t);
        save();
      }
      return { statements: [`db.${t.name}.drop()`], applied: !!body.apply };
    },
  ],
  [
    "POST",
    /^\/services\/([^/]+)\/data\/collections\/([^/]+)\/changes$/,
    ({ m, body }): ChangeResult => {
      const t = db.tables.find((x) => x.name === decodeURIComponent(m[2]));
      if (!t) throw notFound();
      return docChanges(t, body.changes as DocChanges, !!body.apply);
    },
  ],
  [
    "GET",
    /^\/services\/([^/]+)\/data\/keys$/,
    ({ q }): RedisKeys => {
      const re = glob(q.get("pattern") || "*");
      return { keys: db.redis.filter((k) => re.test(k.key)).map((k) => ({ key: k.key, type: k.type, ttl: k.ttl, bytes: JSON.stringify(k.items).length + 48 })), cursor: "0" };
    },
  ],
  [
    "GET",
    /^\/services\/([^/]+)\/data\/key$/,
    ({ q }): RedisValue => {
      const k = db.redis.find((x) => x.key === q.get("key"));
      if (!k) throw notFound();
      // A string's length is its size in bytes, as STRLEN; other types count their items.
      const length = k.type === "string" ? new TextEncoder().encode(k.items[0][0]).length : k.items.length;
      return { type: k.type, length, ttl: k.ttl, items: k.items, truncated: [], cursor: "0" };
    },
  ],
  [
    "POST",
    /^\/services\/([^/]+)\/data\/key\/changes$/,
    ({ body }): ChangeResult => keyChanges(String(body.key), body.changes as KeyChanges, !!body.apply),
  ],
  [
    "POST",
    /^\/services\/([^/]+)\/data\/console$/,
    ({ m, body }) => {
      const kind = serviceOf(m[1]).kind;
      if (kind === "redis") return redisConsole(String(body.query));
      if (kind === "mongodb") return { output: "This demo runs no real MongoDB. Browse shows its documents." };
      return pgConsole(String(body.query));
    },
  ],

  [
    "GET",
    /^\/networks$/,
    (): Network[] =>
      db.networks.map((n) => ({
        ...n,
        dockerName: `kipitiny-net-${n.id}`,
        services: db.services
          .filter((s) => s.networks.includes(n.id))
          .map((s) => {
            const p = projectOf(s.projectId);
            return { id: s.id, name: s.name, kind: s.kind, projectId: p.id, projectName: p.name, alias: `${p.name}-${s.name}` };
          }),
      })),
  ],
  [
    "POST",
    /^\/networks$/,
    ({ body }) => {
      const name = String(body.name ?? "").trim();
      if (!nameRe.test(name)) throw invalid("name must be lowercase letters, digits and dashes (max 40)");
      if (db.networks.some((n) => n.name === name)) throw invalid(`this server already has a network named ${name}`);
      const n = { id: newId("n"), serverId: "local", name, createdAt: now() };
      db.networks.push(n);
      save();
      return { ...n, dockerName: `kipitiny-net-${n.id}`, services: [] };
    },
  ],
  [
    "DELETE",
    /^\/networks\/([^/]+)$/,
    ({ m }) => {
      db.networks = db.networks.filter((n) => n.id !== m[1]);
      for (const s of db.services) s.networks = s.networks.filter((id) => id !== m[1]);
      save();
    },
  ],
];
