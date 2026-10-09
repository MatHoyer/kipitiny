package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

type PgTable struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	// Kind is table, view, materialized view, partitioned table or foreign
	// table.
	Kind string `json:"kind"`
	// RowEstimate comes from the planner statistics; -1 until the table is
	// first analyzed.
	RowEstimate int64 `json:"rowEstimate"`
	Bytes       int64 `json:"bytes"`
}

type PgColumn struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Nullable   bool   `json:"nullable"`
	PrimaryKey bool   `json:"primaryKey"`
}

// PgFilter keeps rows whose Column compares to Value with Op.
type PgFilter struct {
	Column string `json:"column"`
	Op     string `json:"op"`
	Value  string `json:"value,omitempty"`
}

type RowQuery struct {
	Limit   int        `json:"limit"`
	Offset  int        `json:"offset"`
	OrderBy string     `json:"orderBy,omitempty"`
	Desc    bool       `json:"desc,omitempty"`
	Filters []PgFilter `json:"filters,omitempty"`
}

type PgRows struct {
	Columns []PgColumn `json:"columns"`
	// Rows hold each cell as text; nil is NULL.
	Rows [][]*string `json:"rows"`
	// Truncated lists the [row, column] cells cut at DataCellMax.
	Truncated [][2]int `json:"truncated"`
	HasMore   bool     `json:"hasMore"`
}

// pgFilterOps maps the filter operators to SQL; %s are the column and value.
var pgFilterOps = map[string]string{
	"=":       "%s = %s",
	"!=":      "%s <> %s",
	"<":       "%s < %s",
	"<=":      "%s <= %s",
	">":       "%s > %s",
	">=":      "%s >= %s",
	"like":    "%s::text ILIKE %s",
	"null":    "%s IS NULL",
	"notnull": "%s IS NOT NULL",
}

var pgRelKinds = map[string]string{
	"r": "table", "p": "partitioned table", "v": "view", "m": "materialized view", "f": "foreign table",
}

// PgTables lists the tables and views of a postgres service, system schemas
// aside.
func (c *Core) PgTables(ctx context.Context, id string) ([]PgTable, error) {
	if err := Require(ctx, store.ScopeRead); err != nil {
		return nil, err
	}
	t, err := c.dataTarget(ctx, id, store.ServiceKindPostgres)
	if err != nil {
		return nil, err
	}
	tables := []PgTable{}
	err = t.psqlJSON(ctx, `SELECT json_build_array(n.nspname, c.relname, c.relkind, c.reltuples::bigint, pg_total_relation_size(c.oid))
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'p', 'v', 'm', 'f')
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg\_toast%' AND n.nspname NOT LIKE 'pg\_temp%'
ORDER BY n.nspname, c.relname;`, func(row []json.RawMessage) error {
		var tb PgTable
		var kind string
		if err := unmarshalRow(row, &tb.Schema, &tb.Name, &kind, &tb.RowEstimate, &tb.Bytes); err != nil {
			return err
		}
		tb.Kind = pgRelKinds[kind]
		tables = append(tables, tb)
		return nil
	})
	return tables, err
}

// PgRows returns one page of a table's rows with its columns.
func (c *Core) PgRows(ctx context.Context, id, schema, table string, q RowQuery) (PgRows, error) {
	if err := Require(ctx, store.ScopeRead); err != nil {
		return PgRows{}, err
	}
	t, err := c.dataTarget(ctx, id, store.ServiceKindPostgres)
	if err != nil {
		return PgRows{}, err
	}
	cols, err := t.pgColumns(ctx, schema, table)
	if err != nil {
		return PgRows{}, err
	}
	if q.Limit <= 0 || q.Limit > DataPageMax {
		q.Limit = DataPageMax
	}
	sql, err := pgSelect(schema, table, cols, q, true)
	if err != nil {
		return PgRows{}, err
	}
	res := PgRows{Columns: cols, Rows: [][]*string{}, Truncated: [][2]int{}}
	err = t.psqlJSON(ctx, sql, func(row []json.RawMessage) error {
		if len(res.Rows) == q.Limit {
			res.HasMore = true
			return nil
		}
		cells := make([]*string, len(row))
		for j, raw := range row {
			var v *string
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			if v != nil {
				s, cut := truncate(*v)
				if cut {
					res.Truncated = append(res.Truncated, [2]int{len(res.Rows), j})
				}
				v = &s
			}
			cells[j] = v
		}
		res.Rows = append(res.Rows, cells)
		return nil
	})
	return res, err
}

// PgExport streams a table's rows matching q (all of them: no paging) to w
// as CSV with a header.
func (c *Core) PgExport(ctx context.Context, id, schema, table string, q RowQuery, w io.Writer) error {
	if err := Require(ctx, store.ScopeRead); err != nil {
		return err
	}
	t, err := c.dataTarget(ctx, id, store.ServiceKindPostgres)
	if err != nil {
		return err
	}
	cols, err := t.pgColumns(ctx, schema, table)
	if err != nil {
		return err
	}
	sql, err := pgSelect(schema, table, cols, q, false)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, dataExportTimeout)
	defer cancel()
	return clientError(t.dk.Exec(ctx, t.container, t.psql(dataExportTimeout,
		"COPY ("+strings.TrimSuffix(sql, ";")+") TO STDOUT WITH (FORMAT csv, HEADER)", w)))
}

func (t dataTarget) pgColumns(ctx context.Context, schema, table string) ([]PgColumn, error) {
	var cols []PgColumn
	err := t.psqlJSON(ctx, fmt.Sprintf(`SELECT json_build_array(a.attname, format_type(a.atttypid, a.atttypmod), NOT a.attnotnull, coalesce(a.attnum = ANY(i.indkey), false))
FROM pg_attribute a
JOIN pg_class c ON c.oid = a.attrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_index i ON i.indrelid = c.oid AND i.indisprimary
WHERE n.nspname = %s AND c.relname = %s AND c.relkind IN ('r', 'p', 'v', 'm', 'f')
  AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY a.attnum;`, pgLiteral(schema), pgLiteral(table)), func(row []json.RawMessage) error {
		var col PgColumn
		if err := unmarshalRow(row, &col.Name, &col.Type, &col.Nullable, &col.PrimaryKey); err != nil {
			return err
		}
		cols = append(cols, col)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("table %s.%s: %w", schema, table, store.ErrNotFound)
	}
	return cols, nil
}

// pgSelect builds the query for q. Every name it quotes was checked against
// cols; values are literals. page caps cells (as text) and fetches one row
// past q.Limit to tell whether more follow; otherwise the raw columns are
// selected, unbounded.
func pgSelect(schema, table string, cols []PgColumn, q RowQuery, page bool) (string, error) {
	known := make(map[string]bool, len(cols))
	exprs := make([]string, len(cols))
	var pk []string
	for i, col := range cols {
		known[col.Name] = true
		id := pgIdent(col.Name)
		exprs[i] = id
		if page {
			exprs[i] = fmt.Sprintf("left(%s::text, %d)", id, DataCellMax+1)
		}
		if col.PrimaryKey {
			pk = append(pk, id)
		}
	}
	var b strings.Builder
	if page {
		fmt.Fprintf(&b, "SELECT json_build_array(%s)", strings.Join(exprs, ", "))
	} else {
		fmt.Fprintf(&b, "SELECT %s", strings.Join(exprs, ", "))
	}
	fmt.Fprintf(&b, " FROM %s.%s", pgIdent(schema), pgIdent(table))
	for i, f := range q.Filters {
		if !known[f.Column] {
			return "", fmt.Errorf("%w: unknown column %q", ErrInvalid, f.Column)
		}
		op, ok := pgFilterOps[f.Op]
		if !ok {
			return "", fmt.Errorf("%w: unknown filter operator %q", ErrInvalid, f.Op)
		}
		b.WriteString(map[bool]string{true: " WHERE ", false: " AND "}[i == 0])
		if strings.Count(op, "%s") == 1 {
			fmt.Fprintf(&b, op, pgIdent(f.Column))
		} else {
			fmt.Fprintf(&b, op, pgIdent(f.Column), pgLiteral(f.Value))
		}
	}
	order := pk
	if q.OrderBy != "" {
		if !known[q.OrderBy] {
			return "", fmt.Errorf("%w: unknown column %q", ErrInvalid, q.OrderBy)
		}
		order = []string{pgIdent(q.OrderBy)}
	}
	if len(order) > 0 {
		dir := ""
		if q.Desc {
			dir = " DESC"
		}
		b.WriteString(" ORDER BY " + strings.Join(order, dir+", ") + dir)
	}
	if page {
		fmt.Fprintf(&b, " LIMIT %d OFFSET %d", q.Limit+1, max(q.Offset, 0))
	}
	b.WriteString(";")
	return b.String(), nil
}

// psql runs one statement read-only: the session can't write, and long
// queries are cancelled by the server, not only by the context.
func (t dataTarget) psql(timeout time.Duration, sql string, stdout io.Writer) docker.ExecOptions {
	return docker.ExecOptions{
		Cmd: []string{
			"psql", "-X", "-q", "-At", "-v", "ON_ERROR_STOP=1",
			"-U", t.svc.Env[pgUser], "-d", t.svc.Env[pgDatabase], "-f", "-",
		},
		Env: []string{fmt.Sprintf("PGOPTIONS=-c default_transaction_read_only=on -c statement_timeout=%ds",
			int(timeout.Seconds()))},
		Stdin:  strings.NewReader(sql),
		Stdout: stdout,
	}
}

// psqlJSON runs a read-only query whose rows are json_build_array(...), one
// per line, and hands each decoded array to fn.
func (t dataTarget) psqlJSON(ctx context.Context, sql string, fn func([]json.RawMessage) error) error {
	ctx, cancel := context.WithTimeout(ctx, dataBrowseTimeout+5*time.Second)
	defer cancel()
	return t.execLines(ctx, t.psql(dataBrowseTimeout, sql, nil), func(line []byte) error {
		var row []json.RawMessage
		if err := json.Unmarshal(line, &row); err != nil {
			return fmt.Errorf("psql output: %w", err)
		}
		return fn(row)
	})
}

func unmarshalRow(row []json.RawMessage, dst ...any) error {
	if len(row) != len(dst) {
		return fmt.Errorf("psql output: %d fields, want %d", len(row), len(dst))
	}
	for i, raw := range row {
		if err := json.Unmarshal(raw, dst[i]); err != nil {
			return fmt.Errorf("psql output: %w", err)
		}
	}
	return nil
}
