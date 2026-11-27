import type { PgTable } from "../../api";

/** schema.name, how the browser tells tables apart. */
export const tableKey = (t: Pick<PgTable, "schema" | "name">) => `${t.schema}.${t.name}`;
