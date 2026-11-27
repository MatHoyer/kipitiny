package data

import (
	"context"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

// The MySQL and MariaDB data browser speaks the PostgreSQL one's shapes
// (PgTable, PgRows, ConsoleResult): a database is the schema. Browsing and
// the read-only console run as the read-only user; queries the manager
// builds select JSON_ARRAY(...) so each row is one line, and every name is
// checked against information_schema first.

// mysqlSystemDatabases are the server's own, never listed.
var mysqlSystemDatabases = []string{"mysql", "information_schema", "performance_schema", "sys"}

// mysqlBinaryTypes are shown as hex: they hold bytes, not text.
var mysqlBinaryTypes = map[string]bool{
	"binary": true, "varbinary": true, "tinyblob": true, "blob": true, "mediumblob": true, "longblob": true,
	"bit": true, "geometry": true, "point": true, "linestring": true, "polygon": true,
	"multipoint": true, "multilinestring": true, "multipolygon": true, "geometrycollection": true,
}

var mysqlFilterOps = map[string]string{
	"=":       "%s = %s",
	"!=":      "%s <> %s",
	"<":       "%s < %s",
	"<=":      "%s <= %s",
	">":       "%s > %s",
	">=":      "%s >= %s",
	"like":    "CAST(%s AS CHAR) LIKE %s",
	"null":    "%s IS NULL",
	"notnull": "%s IS NOT NULL",
}

// mysqlString is a string literal that can't break out of its quotes
// whatever the SQL mode: hex, with an introducer so it takes the
// collation of what it's compared to.
func mysqlString(s string) string {
	return "_utf8mb4 X'" + hex.EncodeToString([]byte(s)) + "'"
}

// mysql runs sql as user (the read-only one unless root), one JSON line or
// tab-separated row per result row, with statements cut after timeout.
func (t dataTarget) mysql(sql string, root bool, timeout time.Duration, stdout io.Writer, extra ...string) docker.ExecOptions {
	user, pw := readUser, readUserPassword(t.svc.Env[MySQLRootPassword])
	if root {
		user, pw = "root", t.svc.Env[MySQLRootPassword]
	}
	// max_execution_time (MySQL, ms) only bounds SELECT; MariaDB's
	// max_statement_time (s) bounds every statement.
	limit := fmt.Sprintf("SET SESSION max_execution_time = %d", timeout.Milliseconds())
	if t.svc.Kind == domain.ServiceKindMariaDB {
		limit = fmt.Sprintf("SET SESSION max_statement_time = %d", int(timeout.Seconds()))
	}
	cmd := append([]string{
		MySQLBin(t.svc.Kind, "mysql"), "-u" + user, "--default-character-set=utf8mb4", "--init-command=" + limit,
	}, extra...)
	return docker.ExecOptions{
		Cmd:    append(cmd, "-D", t.db),
		Env:    []string{"MYSQL_PWD=" + pw},
		Stdin:  strings.NewReader(sql),
		Stdout: stdout,
	}
}

// mysqlJSON runs a read-only query whose rows are JSON_ARRAY(...), one per
// line, and hands each decoded array to fn.
func (t dataTarget) mysqlJSON(ctx context.Context, sql string, fn func([]json.RawMessage) error) error {
	ctx, cancel := context.WithTimeout(ctx, dataBrowseTimeout+5*time.Second)
	defer cancel()
	// Raw: JSON escapes newlines itself, and batch mode would double its
	// backslashes.
	return t.execLines(ctx, t.mysql(sql, false, dataBrowseTimeout, nil, "-N", "-B", "--raw"), func(line []byte) error {
		var row []json.RawMessage
		if err := json.Unmarshal(line, &row); err != nil {
			return fmt.Errorf("mysql output: %w", err)
		}
		return fn(row)
	})
}

// flag reads a JSON_ARRAY boolean: MySQL writes true, MariaDB 1.
type flag bool

func (f *flag) UnmarshalJSON(b []byte) error {
	*f = string(b) == "true" || string(b) == "1"
	return nil
}

func (t dataTarget) mysqlDatabases(ctx context.Context) ([]PgDatabase, error) {
	dbs := []PgDatabase{}
	skip := make([]string, len(mysqlSystemDatabases))
	for i, name := range mysqlSystemDatabases {
		skip[i] = mysqlString(name)
	}
	err := t.mysqlJSON(ctx, fmt.Sprintf(`SELECT JSON_ARRAY(s.SCHEMA_NAME, CAST(COALESCE(SUM(t.DATA_LENGTH + t.INDEX_LENGTH), 0) AS SIGNED))
FROM information_schema.SCHEMATA s LEFT JOIN information_schema.TABLES t ON t.TABLE_SCHEMA = s.SCHEMA_NAME
WHERE s.SCHEMA_NAME NOT IN (%s) AND s.SCHEMA_NAME NOT LIKE '%%\_\_restore'
GROUP BY s.SCHEMA_NAME ORDER BY s.SCHEMA_NAME`, strings.Join(skip, ", ")), func(row []json.RawMessage) error {
		var d PgDatabase
		if err := unmarshalRow(row, &d.Name, &d.Bytes); err != nil {
			return err
		}
		d.Main = d.Name == t.svc.Env[mysqlDatabase]
		dbs = append(dbs, d)
		return nil
	})
	return dbs, err
}

// mysqlCreateDatabase creates a database the app user owns. The name was
// checked like a PostgreSQL one.
func (t dataTarget) mysqlCreateDatabase(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, dataConsoleTimeout)
	defer cancel()
	// The user name is a quoted literal; with NO_BACKSLASH_ESCAPES only its
	// quotes need doubling, whatever the server's own mode.
	sql := fmt.Sprintf("SET SESSION sql_mode = CONCAT_WS(',', NULLIF(@@SESSION.sql_mode, ''), 'NO_BACKSLASH_ESCAPES'); "+
		"CREATE DATABASE %s; GRANT ALL ON %s.* TO %s@'%%'",
		mysqlIdent(name), mysqlIdent(name), mysqlQuote(t.svc.Env[mysqlUser]))
	return clientError(t.dk.Exec(ctx, t.container, t.mysql(sql, true, dataConsoleTimeout, nil)))
}

type mysqlColumn struct {
	PgColumn
	binary bool
}

func (t dataTarget) mysqlTables(ctx context.Context) ([]PgTable, error) {
	tables := []PgTable{}
	byName := map[string]int{}
	err := t.mysqlJSON(ctx, `SELECT JSON_ARRAY('t', TABLE_NAME, TABLE_TYPE, CAST(COALESCE(TABLE_ROWS, -1) AS SIGNED),
  CAST(COALESCE(DATA_LENGTH + INDEX_LENGTH, 0) AS SIGNED))
FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() ORDER BY TABLE_NAME;
SELECT JSON_ARRAY('c', TABLE_NAME, COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE = 'YES', COLUMN_KEY = 'PRI')
FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() ORDER BY TABLE_NAME, ORDINAL_POSITION;
SELECT JSON_ARRAY('f', TABLE_NAME, MIN(COLUMN_NAME), MIN(REFERENCED_TABLE_NAME), MIN(REFERENCED_COLUMN_NAME))
FROM information_schema.KEY_COLUMN_USAGE
WHERE TABLE_SCHEMA = DATABASE() AND REFERENCED_TABLE_SCHEMA = DATABASE()
GROUP BY TABLE_NAME, CONSTRAINT_NAME HAVING COUNT(*) = 1 ORDER BY TABLE_NAME, CONSTRAINT_NAME;`, func(row []json.RawMessage) error {
		var tag string
		if len(row) == 0 || json.Unmarshal(row[0], &tag) != nil {
			return errors.New("mysql output: no tag")
		}
		if tag == "t" {
			tb := PgTable{Schema: t.db, Columns: []PgColumn{}}
			var kind string
			if err := unmarshalRow(row[1:], &tb.Name, &kind, &tb.RowEstimate, &tb.Bytes); err != nil {
				return err
			}
			tb.Kind = mysqlTableKind(kind)
			if tb.Kind == "view" {
				tb.RowEstimate = -1
			}
			byName[tb.Name] = len(tables)
			tables = append(tables, tb)
			return nil
		}
		if tag == "f" {
			// Single-column foreign keys within the database.
			var table, column string
			var ref PgRef
			if err := unmarshalRow(row[1:], &table, &column, &ref.Table, &ref.Column); err != nil {
				return err
			}
			ref.Schema = t.db
			if i, ok := byName[table]; ok {
				for j := range tables[i].Columns {
					if c := &tables[i].Columns[j]; c.Name == column && c.References == nil {
						c.References = &ref
					}
				}
			}
			return nil
		}
		var table string
		var col PgColumn
		var nullable, pk flag
		if err := unmarshalRow(row[1:], &table, &col.Name, &col.Type, &nullable, &pk); err != nil {
			return err
		}
		col.Nullable, col.PrimaryKey = bool(nullable), bool(pk)
		if i, ok := byName[table]; ok {
			tables[i].Columns = append(tables[i].Columns, col)
		}
		return nil
	})
	return tables, err
}

func mysqlTableKind(t string) string {
	switch t {
	case "BASE TABLE", "SYSTEM VERSIONED":
		return "table"
	case "VIEW", "SYSTEM VIEW":
		return "view"
	}
	return strings.ToLower(t)
}

func (t dataTarget) mysqlColumns(ctx context.Context, table string) ([]mysqlColumn, error) {
	var cols []mysqlColumn
	err := t.mysqlJSON(ctx, fmt.Sprintf(`SELECT JSON_ARRAY(COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE = 'YES', COLUMN_KEY = 'PRI', DATA_TYPE)
FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = %s ORDER BY ORDINAL_POSITION`, mysqlString(table)),
		func(row []json.RawMessage) error {
			var col mysqlColumn
			var nullable, pk flag
			var dataType string
			if err := unmarshalRow(row, &col.Name, &col.Type, &nullable, &pk, &dataType); err != nil {
				return err
			}
			col.Nullable, col.PrimaryKey, col.binary = bool(nullable), bool(pk), mysqlBinaryTypes[strings.ToLower(dataType)]
			cols = append(cols, col)
			return nil
		})
	if err != nil {
		return nil, err
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("table %s.%s: %w", t.db, table, domain.ErrNotFound)
	}
	return cols, nil
}

// mysqlSelect builds the query for q, like pgSelect: every name it quotes
// was checked against cols, values are hex literals. page caps cells and
// fetches one row past q.Limit; otherwise whole cells, unbounded.
func (t dataTarget) mysqlSelect(table string, cols []mysqlColumn, q RowQuery, page bool) (string, error) {
	known := make(map[string]bool, len(cols))
	exprs := make([]string, len(cols))
	var pk []string
	for i, col := range cols {
		known[col.Name] = true
		id := mysqlIdent(col.Name)
		switch {
		case col.binary && page:
			exprs[i] = fmt.Sprintf("CONCAT('0x', HEX(LEFT(%s, %d)))", id, DataCellMax/2)
		case col.binary:
			exprs[i] = fmt.Sprintf("CONCAT('0x', HEX(%s))", id)
		case page:
			exprs[i] = fmt.Sprintf("LEFT(CAST(%s AS CHAR), %d)", id, DataCellMax+1)
		default:
			exprs[i] = fmt.Sprintf("CAST(%s AS CHAR)", id)
		}
		if col.PrimaryKey {
			pk = append(pk, id)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "SELECT JSON_ARRAY(%s) FROM %s.%s", strings.Join(exprs, ", "), mysqlIdent(t.db), mysqlIdent(table))
	var where []string
	for _, f := range q.Filters {
		if !known[f.Column] {
			return "", fmt.Errorf("%w: unknown column %q", ErrInvalid, f.Column)
		}
		op, ok := mysqlFilterOps[f.Op]
		if !ok {
			return "", fmt.Errorf("%w: unknown filter operator %q", ErrInvalid, f.Op)
		}
		if strings.Count(op, "%s") == 1 {
			where = append(where, fmt.Sprintf(op, mysqlIdent(f.Column)))
		} else {
			where = append(where, fmt.Sprintf(op, mysqlIdent(f.Column), mysqlString(f.Value)))
		}
	}
	if q.Search != "" {
		// Matched literally: LIKE's own wildcards are escaped (\ is its
		// default escape character).
		pattern := mysqlString("%" + likeEscaper.Replace(q.Search) + "%")
		any := make([]string, len(cols))
		for i, col := range cols {
			any[i] = fmt.Sprintf("CAST(%s AS CHAR) LIKE %s", mysqlIdent(col.Name), pattern)
		}
		where = append(where, "("+strings.Join(any, " OR ")+")")
	}
	if len(where) > 0 {
		b.WriteString(" WHERE " + strings.Join(where, " AND "))
	}
	order := pk
	if q.OrderBy != "" {
		if !known[q.OrderBy] {
			return "", fmt.Errorf("%w: unknown column %q", ErrInvalid, q.OrderBy)
		}
		order = []string{mysqlIdent(q.OrderBy)}
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
	return b.String(), nil
}

func (t dataTarget) mysqlRows(ctx context.Context, schema, table string, q RowQuery) (PgRows, error) {
	if schema != "" && schema != t.db {
		return PgRows{}, fmt.Errorf("table %s.%s: %w", schema, table, domain.ErrNotFound)
	}
	cols, err := t.mysqlColumns(ctx, table)
	if err != nil {
		return PgRows{}, err
	}
	if q.Limit <= 0 || q.Limit > DataPageMax {
		q.Limit = DataPageMax
	}
	sql, err := t.mysqlSelect(table, cols, q, true)
	if err != nil {
		return PgRows{}, err
	}
	res := PgRows{Columns: make([]PgColumn, len(cols)), Rows: [][]*string{}, Truncated: [][2]int{}}
	for i, col := range cols {
		res.Columns[i] = col.PgColumn
	}
	err = t.mysqlJSON(ctx, sql, func(row []json.RawMessage) error {
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
				if cut || (cols[j].binary && len(s) >= DataCellMax) {
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

// mysqlExport streams the rows matching q to w as CSV with a header; NULL
// is an empty cell.
func (t dataTarget) mysqlExport(ctx context.Context, schema, table string, q RowQuery, w io.Writer) error {
	if schema != "" && schema != t.db {
		return fmt.Errorf("table %s.%s: %w", schema, table, domain.ErrNotFound)
	}
	cols, err := t.mysqlColumns(ctx, table)
	if err != nil {
		return err
	}
	sql, err := t.mysqlSelect(table, cols, q, false)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, dataExportTimeout)
	defer cancel()
	cw := csv.NewWriter(w)
	header := make([]string, len(cols))
	for i, col := range cols {
		header[i] = col.Name
	}
	wroteHeader := false
	record := make([]string, len(cols))
	err = t.execLines(ctx, t.mysql(sql, false, dataExportTimeout, nil, "-N", "-B", "--raw"), func(line []byte) error {
		if !wroteHeader {
			wroteHeader = true
			if err := cw.Write(header); err != nil {
				return err
			}
		}
		var row []*string
		if err := json.Unmarshal(line, &row); err != nil {
			return fmt.Errorf("mysql output: %w", err)
		}
		for i, v := range row {
			record[i] = ""
			if v != nil {
				record[i] = *v
			}
		}
		return cw.Write(record)
	})
	if err != nil {
		return err
	}
	if !wroteHeader {
		if err := cw.Write(header); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// xmlSafeReader replaces what XML 1.0 can't hold (invalid UTF-8, control
// characters) with U+FFFD, so a cell of odd bytes doesn't stop the parse.
type xmlSafeReader struct {
	r    io.Reader
	tail []byte // an incomplete rune from the last read
	out  []byte
}

func (x *xmlSafeReader) Read(p []byte) (int, error) {
	for len(x.out) == 0 {
		buf := make([]byte, 32<<10)
		n, err := x.r.Read(buf)
		in := slices.Concat(x.tail, buf[:n])
		x.tail = nil
		for len(in) > 0 {
			r, size := utf8.DecodeRune(in)
			if r == utf8.RuneError && size <= 1 && !utf8.FullRune(in) && err == nil {
				x.tail = append([]byte(nil), in...)
				break
			}
			if (r < 0x20 && r != '\t' && r != '\n' && r != '\r') || r == utf8.RuneError || r == 0xFFFE || r == 0xFFFF {
				x.out = utf8.AppendRune(x.out, utf8.RuneError)
			} else {
				x.out = append(x.out, in[:size]...)
			}
			in = in[size:]
		}
		if err != nil {
			if len(x.out) == 0 {
				return 0, err
			}
			break
		}
	}
	n := copy(p, x.out)
	x.out = x.out[n:]
	return n, nil
}
