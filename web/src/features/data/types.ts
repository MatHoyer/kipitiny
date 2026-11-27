/** A database of a postgres instance; main is the service's own (POSTGRES_DB). */
export type PgDatabase = { name: string; bytes: number; main: boolean };

/** Data browser: a postgres table or view. rowEstimate is -1 until the table is analyzed. */
export type PgTable = { schema: string; name: string; kind: string; rowEstimate: number; bytes: number; columns: PgColumn[] };

/** references: the column a single-column foreign key points to, in table listings only. */
export type PgColumn = { name: string; type: string; nullable: boolean; primaryKey: boolean; references?: PgRef };

export type PgRef = { schema: string; table: string; column: string };

export type PgFilterOp = "=" | "!=" | "<" | "<=" | ">" | ">=" | "like" | "null" | "notnull";

export type PgFilter = { column: string; op: PgFilterOp; value?: string };

export type RowQuery = { limit?: number; offset?: number; orderBy?: string; desc?: boolean; filters?: PgFilter[]; search?: string };

/** Cells are text, null for NULL; truncated lists the [row, column] cells cut short. */
/** One page of a MongoDB collection: each document as relaxed Extended JSON. */
export type MongoDocs = { documents: string[]; truncated: number[]; hasMore: boolean };

export type DocQuery = { filter?: string; sort?: string; limit?: number; skip?: number };

/**
 * Edits to a table's rows, saved together. key holds the primary key as shown; was, each set column as loaded (null is NULL).
 * An insert's columns left out take their default.
 */
export type RowChanges = {
  deletes?: { key: Record<string, string> }[];
  updates?: { key: Record<string, string>; set: Record<string, string | null>; was: Record<string, string | null> }[];
  inserts?: Record<string, string | null>[];
};

/**
 * Edits to a collection's documents, values as relaxed Extended JSON. An update sets or unsets top-level fields;
 * was holds each one as loaded, null when it was absent.
 */
export type DocChanges = {
  deletes?: { id: string }[];
  updates?: { id: string; set: Record<string, string>; unset: string[]; was: Record<string, string | null> }[];
  inserts?: string[];
};

/**
 * Edits to a Redis key; items as RedisValue gives them. type is the key's as loaded. delete removes the key, alone;
 * create makes a new key of type from inserts. A list appends its inserts (their index is ignored); a stream's
 * entries can only be deleted.
 */
export type KeyChanges = {
  type: string;
  create?: boolean;
  delete?: boolean;
  edits?: { item: string[]; set: string[] }[];
  deletes?: string[][];
  inserts?: string[][];
};

/** A column of a table being created: type is the database's own; default an SQL expression. */
export type NewColumn = { name: string; type: string; nullable: boolean; primaryKey: boolean; default?: string };

/** The statements a save runs, in order; applied is false for a preview. */
export type ChangeResult = { statements: string[]; applied: boolean };

export type PgRows = { columns: PgColumn[]; rows: (string | null)[][]; truncated: [number, number][]; hasMore: boolean };

/** ttl in ms, -1 when the key never expires. */
export type RedisKey = { key: string; type: string; ttl: number; bytes: number };

/** cursor "0" once the scan is complete. */
export type RedisKeys = { keys: RedisKey[]; cursor: string };

/** items by type: [value], [field, value], [index, value], [member], [member, score], [id, field, value, ...]. */
export type RedisValue = { type: string; length: number; ttl: number; items: string[][]; truncated: [number, number][]; cursor: string };

export type ConsoleResult = {
  columns?: string[];
  rows?: (string | null)[][];
  truncated?: [number, number][];
  more?: boolean;
  output?: string;
};
