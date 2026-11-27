import type { ConsoleResult } from "@/api";
import type { PgFilter } from "@/api";
import type { PgRows } from "@/api";
import type { RowChanges, DocChanges, KeyChanges, ChangeResult } from "@/api";
import { type DemoDb } from "./seed";

import { db, invalid, save } from "./state";

/** Saves key edits like the manager does: every check first, then the writes, as one script would. */
export function keyChanges(key: string, ch: KeyChanges, apply: boolean): ChangeResult {
  if (ch.create) {
    if (db.redis.some((x) => x.key === key)) throw invalid(`key ${JSON.stringify(key)} already exists`);
    const ins = ch.inserts ?? [];
    const q = (a: string) => (a && !/[\s"'\\]/.test(a) ? a : JSON.stringify(a));
    const verb = { string: "SET", hash: "HSET", list: "RPUSH", set: "SADD", zset: "ZADD" }[ch.type as "string"];
    const statements = ins.map((it) => [verb, key, ...(ch.type === "list" ? [it[1]] : ch.type === "zset" ? [it[1], it[0]] : it)].map(q).join(" "));
    if (apply) {
      const items = ch.type === "list" ? ins.map((it, i) => [String(i), it[1]]) : ins;
      db.redis.push({ key, type: ch.type as DemoDb["redis"][number]["type"], ttl: -1, items });
      save();
    }
    return { statements, applied: apply };
  }
  const k = db.redis.find((x) => x.key === key);
  if (!k || k.type !== ch.type) throw invalid("the key changed or was deleted since it was loaded; reload and make the change again");
  const arg = (a: string) => (a && !/[\s"'\\]/.test(a) ? a : JSON.stringify(a));
  const cmd = (...a: string[]) => a.map(arg).join(" ");
  const statements: string[] = [];
  let items = k.items.map((it) => [...it]);
  const find = (first: string) => {
    const it = items.find((x) => x[0] === first);
    if (!it) throw invalid(`${first} changed or was deleted since it was loaded; reload and make the change again`);
    return it;
  };
  if (ch.delete) statements.push(cmd("DEL", key));
  else if (k.type === "string") {
    const e = ch.edits![0];
    statements.push(cmd("SET", key, e.set[0], "KEEPTTL"));
    items = [[e.set[0]]];
  } else {
    for (const e of ch.edits ?? []) {
      const it = find(e.item[0]);
      if (k.type === "hash") statements.push(cmd("HSET", key, e.set[0], e.set[1]));
      if (k.type === "list") statements.push(cmd("LSET", key, e.item[0], e.set[1]));
      if (k.type === "set") statements.push(cmd("SREM", key, e.item[0]), cmd("SADD", key, e.set[0]));
      if (k.type === "zset") statements.push(...(e.set[0] === e.item[0] ? [cmd("ZADD", key, "XX", e.set[1], e.set[0])] : [cmd("ZREM", key, e.item[0]), cmd("ZADD", key, e.set[1], e.set[0])]));
      it.splice(0, it.length, ...e.set);
    }
    const gone = new Set((ch.deletes ?? []).map((d) => d[0]));
    for (const d of ch.deletes ?? []) {
      find(d[0]);
      statements.push(k.type === "hash" ? cmd("HDEL", key, d[0]) : k.type === "set" ? cmd("SREM", key, d[0]) : k.type === "zset" ? cmd("ZREM", key, d[0]) : cmd("LSET", key, d[0], "__kipitiny_deleted"));
    }
    if (k.type === "list" && gone.size) statements.push(cmd("LREM", key, "0", "__kipitiny_deleted"));
    items = items.filter((it) => !gone.has(it[0]));
    for (const ins of ch.inserts ?? []) {
      statements.push(k.type === "hash" ? cmd("HSET", key, ins[0], ins[1]) : k.type === "list" ? cmd("RPUSH", key, ins[1]) : k.type === "set" ? cmd("SADD", key, ins[0]) : cmd("ZADD", key, ins[1], ins[0]));
      items.push(k.type === "list" ? ["", ins[1]] : [...ins]);
    }
    if (k.type === "list") items = items.map((it, i) => [String(i), it[1]]);
  }
  if (!statements.length) throw invalid("no changes to save");
  if (apply) {
    if (ch.delete) db.redis = db.redis.filter((x) => x !== k);
    else k.items = items;
    save();
  }
  return { statements, applied: apply };
}

/** The demo's documents are its tables' rows; _id is the row's position. */
export const demoOid = (i: number) => ({ $oid: `6aca9c0c13d4e72f99${String(i).padStart(6, "0")}` });

/** Saves document edits like the manager does, onto the demo table behind the collection. */
export function docChanges(t: DemoDb["tables"][number], ch: DocChanges, apply: boolean): ChangeResult {
  const coll = /^[A-Za-z_][A-Za-z0-9_]*$/.test(t.name) ? `db.${t.name}` : `db.getCollection(${JSON.stringify(t.name)})`;
  const obj = (fields: [string, string][]) => `{${fields.map(([k, v]) => `${JSON.stringify(k)}: ${v}`).join(", ")}}`;
  const indexOf = (id: string) => t.rows.findIndex((_, i) => JSON.stringify(demoOid(i)) === JSON.stringify(JSON.parse(id)));
  const statements: string[] = [];
  const rows = t.rows.map((r) => [...r]);
  const gone = new Set<number>();
  for (const d of ch.deletes ?? []) {
    statements.push(`${coll}.deleteOne(${obj([["_id", d.id]])})`);
    gone.add(indexOf(d.id));
  }
  for (const u of ch.updates ?? []) {
    const names = Object.keys(u.set).sort();
    const filter = obj([["_id", u.id], ...[...names, ...u.unset].map((k): [string, string] => [k, u.was[k] ?? '{"$exists": false}'])]);
    const update = obj([...(names.length ? [["$set", obj(names.map((k) => [k, u.set[k]]))] as [string, string]] : []), ...(u.unset.length ? [["$unset", obj(u.unset.map((k) => [k, '""']))] as [string, string]] : [])]);
    statements.push(`${coll}.updateOne(${filter}, ${update})`);
    const r = rows[indexOf(u.id)];
    if (!r) throw invalid(`document ${u.id} changed or was deleted since it was loaded; reload and make the change again`);
    for (const [k, v] of Object.entries(u.set)) {
      const j = t.columns.findIndex((c) => c.name === k);
      if (j >= 0) r[j] = JSON.parse(v) === null ? null : String(JSON.parse(v));
    }
    for (const k of u.unset) {
      const j = t.columns.findIndex((c) => c.name === k);
      if (j >= 0) r[j] = null;
    }
  }
  for (const doc of ch.inserts ?? []) {
    statements.push(`${coll}.insertOne(${doc})`);
    const d = JSON.parse(doc) as Record<string, unknown>;
    rows.push(t.columns.map((c) => (d[c.name] === undefined || d[c.name] === null ? null : String(d[c.name]))));
  }
  if (!statements.length) throw invalid("no changes to save");
  if (apply) {
    t.rows = rows.filter((_, i) => !gone.has(i));
    save();
  }
  return { statements, applied: apply };
}

/** Saves edits from the browser like the manager does: checked rows, statements as it writes them. */
export function tableChanges(t: DemoDb["tables"][number], ch: RowChanges, apply: boolean, postgres: boolean): ChangeResult {
  const q = (s: string) => (postgres ? `"${s.replace(/"/g, '""')}"` : `\`${s.replace(/`/g, "``")}\``);
  const lit = (v: string | null) => (v === null ? "NULL" : `'${v.replace(/'/g, "''")}'`);
  const name = postgres ? `${q(t.schema)}.${q(t.name)}` : q(t.name);
  const col = (c: string) => {
    const i = t.columns.findIndex((x) => x.name === c);
    if (i < 0) throw invalid(`unknown column "${c}"`);
    return i;
  };
  const text = (c: string, v: string | null) => (v === null ? `${q(c)} IS NULL` : postgres ? `${q(c)}::text = ${lit(v)}` : `CAST(${q(c)} AS CHAR) COLLATE utf8mb4_bin = ${lit(v)}`);
  const where = (key: Record<string, string>) => Object.entries(key).map(([c, v]) => `${q(c)} = ${lit(v)}`);
  const find = (key: Record<string, string>, label: string, was: Record<string, string | null> = {}) => {
    const r = rows.find((r) => Object.entries(key).every(([c, v]) => r[col(c)] === v) && Object.entries(was).every(([c, v]) => r[col(c)] === v));
    if (!r) throw invalid(`${label} changed or was deleted since it was loaded; reload and make the change again`);
    return r;
  };
  const label = (key: Record<string, string>) => "row " + Object.entries(key).map(([c, v]) => `${c} ${v}`).join(", ");
  // Changed on a copy, kept only once every statement went through: one transaction.
  const rows = t.rows.map((r) => [...r]);
  const statements: string[] = [];
  for (const d of ch.deletes ?? []) {
    statements.push(`DELETE FROM ${name} WHERE ${where(d.key).join(" AND ")};`);
    rows.splice(rows.indexOf(find(d.key, label(d.key))), 1);
  }
  for (const u of ch.updates ?? []) {
    const set = Object.entries(u.set).map(([c, v]) => `${q(c)} = ${lit(v)}`);
    const conds = [...where(u.key), ...Object.entries(u.was).map(([c, v]) => text(c, v))];
    statements.push(`UPDATE ${name} SET ${set.join(", ")} WHERE ${conds.join(" AND ")};`);
    const r = find(u.key, label(u.key), u.was);
    for (const [c, v] of Object.entries(u.set)) r[col(c)] = v;
  }
  for (const ins of ch.inserts ?? []) {
    const cs = Object.keys(ins);
    statements.push(cs.length ? `INSERT INTO ${name} (${cs.map(q).join(", ")}) VALUES (${cs.map((c) => lit(ins[c])).join(", ")});` : postgres ? `INSERT INTO ${name} DEFAULT VALUES;` : `INSERT INTO ${name} () VALUES ();`);
    const pk = t.columns.findIndex((c) => c.primaryKey);
    rows.push(t.columns.map((c, i) => (c.name in ins ? ins[c.name] : i === pk ? String(Math.max(0, ...rows.map((r) => Number(r[pk]) || 0)) + 1) : null)));
  }
  if (!statements.length) throw invalid("no changes to save");
  if (apply) {
    t.rows = rows;
    save();
  }
  return { statements, applied: apply };
}

export function rows(table: DemoDb["tables"][number], q: URLSearchParams): PgRows {
  let rs = table.rows;
  const filters = JSON.parse(q.get("filters") || "[]") as PgFilter[];
  for (const f of filters) {
    const i = table.columns.findIndex((c) => c.name === f.column);
    if (i < 0) continue;
    const num = (v: string | null) => (v !== null && v !== "" && !isNaN(Number(v)) ? Number(v) : v);
    rs = rs.filter((r) => {
      const v = r[i];
      switch (f.op) {
        case "null":
          return v === null;
        case "notnull":
          return v !== null;
        case "like":
          return v !== null && new RegExp(`^${(f.value ?? "").replace(/[.*+?^${}()|[\]\\]/g, "\\$&").replace(/%/g, ".*").replace(/_/g, ".")}$`, "i").test(v);
        case "=":
          return v === f.value;
        case "!=":
          return v !== f.value;
        default: {
          const a = num(v), b = num(f.value ?? "");
          if (a === null || b === null) return false;
          return f.op === "<" ? a < b : f.op === "<=" ? a <= b : f.op === ">" ? a > b : a >= b;
        }
      }
    });
  }
  const search = q.get("search")?.toLowerCase();
  if (search) rs = rs.filter((r) => r.some((v) => v?.toLowerCase().includes(search)));
  const order = q.get("order");
  const oi = order ? table.columns.findIndex((c) => c.name === order) : -1;
  if (oi >= 0) {
    const desc = q.get("desc") === "true";
    rs = [...rs].sort((a, b) => {
      const x = a[oi], y = b[oi];
      if (x === y) return 0;
      if (x === null) return 1;
      if (y === null) return -1;
      const c = !isNaN(Number(x)) && !isNaN(Number(y)) ? Number(x) - Number(y) : x.localeCompare(y);
      return desc ? -c : c;
    });
  }
  const limit = Number(q.get("limit")) || 50;
  const offset = Number(q.get("offset")) || 0;
  return {
    columns: table.columns.map((c) => ({ name: c.name, type: c.type, nullable: !!c.nullable, primaryKey: !!c.primaryKey })),
    rows: rs.slice(offset, offset + limit),
    truncated: [],
    hasMore: offset + limit < rs.length,
  };
}

/** The few statements the demo understands; anything else explains itself. */
export function pgConsole(query: string): ConsoleResult {
  const q = query.trim().replace(/;$/, "");
  const count = /^select\s+count\(\*\)\s+from\s+(?:public\.)?(\w+)$/i.exec(q);
  if (count) {
    const t = db.tables.find((x) => x.name === count[1]);
    if (!t) return { output: `ERROR:  relation "${count[1]}" does not exist` };
    return { columns: ["count"], rows: [[String(t.rows.length)]] };
  }
  const sel = /^select\s+\*\s+from\s+(?:public\.)?(\w+)(?:\s+limit\s+(\d+))?$/i.exec(q);
  if (sel) {
    const t = db.tables.find((x) => x.name === sel[1]);
    if (!t) return { output: `ERROR:  relation "${sel[1]}" does not exist` };
    const n = Math.min(Number(sel[2] ?? 50), 200);
    return { columns: t.columns.map((c) => c.name), rows: t.rows.slice(0, n), more: t.rows.length > n };
  }
  return { output: "This demo runs no real PostgreSQL. Try: SELECT * FROM users LIMIT 10, or SELECT count(*) FROM invoices." };
}

export const glob = (pattern: string) => new RegExp(`^${pattern.replace(/[.+^${}()|[\]\\]/g, "\\$&").replace(/\*/g, ".*").replace(/\?/g, ".")}$`);

export function redisConsole(command: string): ConsoleResult {
  const [cmd = "", ...args] = command.trim().split(/\s+/);
  const key = db.redis.find((k) => k.key === args[0]);
  switch (cmd.toUpperCase()) {
    case "PING":
      return { output: "PONG" };
    case "DBSIZE":
      return { output: `(integer) ${db.redis.length}` };
    case "KEYS":
      return { output: db.redis.filter((k) => glob(args[0] ?? "*").test(k.key)).map((k, i) => `${i + 1}) "${k.key}"`).join("\n") || "(empty array)" };
    case "GET":
      return { output: key?.type === "string" ? `"${key.items[0][0]}"` : "(nil)" };
    case "TYPE":
      return { output: key?.type ?? "none" };
    case "TTL":
      return { output: `(integer) ${key ? (key.ttl < 0 ? -1 : Math.round(key.ttl / 1000)) : -2}` };
    case "HGETALL":
      return { output: key?.type === "hash" ? key.items.flat().map((v, i) => `${i + 1}) "${v}"`).join("\n") : "(empty array)" };
  }
  return { output: "This demo runs no real Redis. Try: KEYS *, GET cache:plans, HGETALL session:…, TTL rate:203.0.113.10." };
}
