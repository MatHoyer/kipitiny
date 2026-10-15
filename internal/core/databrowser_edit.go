package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// Edits from the data browser: the caller sends what changed, the manager
// writes the statements (every name checked against the table, values as
// literals) and runs them as one transaction. Each update and delete must
// hit exactly one row, found by its primary key and, for an update, by the
// edited columns' values as they were loaded: a row someone changed in the
// meantime fails the whole save instead of being overwritten.

// RowChanges are edits to one table's rows. They run deletes first, then
// updates, then inserts.
type RowChanges struct {
	Deletes []RowDelete `json:"deletes,omitempty"`
	Updates []RowUpdate `json:"updates,omitempty"`
	// Inserts give a value per column (nil is NULL); columns left out take
	// their default.
	Inserts []map[string]*string `json:"inserts,omitempty"`
}

type RowDelete struct {
	// Key holds the primary key's columns as text, as the browser shows them.
	Key map[string]string `json:"key"`
}

type RowUpdate struct {
	Key map[string]string  `json:"key"`
	Set map[string]*string `json:"set"`
	// Was holds every Set column's value as it was loaded (nil is NULL).
	Was map[string]*string `json:"was"`
}

// ChangeResult lists the statements of a save, in order. Applied is false
// for a preview.
type ChangeResult struct {
	Statements []string `json:"statements"`
	Applied    bool     `json:"applied"`
}

// dataChangesMax caps the changes of one save.
const dataChangesMax = 1000

// rowChanged is why a save stopped at an update or delete.
func rowChanged(label string) string {
	return label + " changed or was deleted since it was loaded; reload and make the change again"
}

// ApplyRowChanges saves edits to a table of a PostgreSQL, MySQL or MariaDB
// database, or with apply unset only returns the statements it would run.
// Admin only.
func (c *Core) ApplyRowChanges(ctx context.Context, id, database, schema, table string, ch RowChanges, apply bool) (res ChangeResult, err error) {
	if err := c.requireOn(ctx, id, PermDataEdit); err != nil {
		return ChangeResult{}, err
	}
	if apply {
		defer func() {
			status := http.StatusOK
			if err != nil {
				status = http.StatusBadRequest
			}
			c.Audit(ctx, "data edit", id, status, err)
		}()
	}
	t, err := c.sqlTarget(ctx, id, database)
	if err != nil {
		return ChangeResult{}, err
	}
	if t.svc.Kind.IsMySQL() {
		return t.mysqlApply(ctx, schema, table, ch, apply)
	}
	return t.pgApply(ctx, schema, table, ch, apply)
}

// editStmt is one statement of a save. where, for an update or delete, is
// its condition, which must match exactly one row; label names that row.
type editStmt struct {
	sql, where, label string
}

// sqlEdit is what tells PostgreSQL's statements from MySQL's.
type sqlEdit struct {
	table   string // quoted, qualified as needed
	ident   func(string) string
	literal func(string) string
	// textEq compares a column, as the text the browser shows, to a literal.
	textEq string
	// noColumns inserts a row of defaults only.
	noColumns string
	// locked are columns that can't be written, with the reason.
	locked map[string]string
}

// editStatements turns changes into statements: deletes, updates, inserts.
func editStatements(d sqlEdit, cols []PgColumn, ch RowChanges) ([]editStmt, error) {
	if n := len(ch.Deletes) + len(ch.Updates) + len(ch.Inserts); n == 0 {
		return nil, fmt.Errorf("%w: no changes to save", ErrInvalid)
	} else if n > dataChangesMax {
		return nil, fmt.Errorf("%w: %d changes at once is too many; save at most %d", ErrInvalid, n, dataChangesMax)
	}
	known := make(map[string]bool, len(cols))
	var pk []string
	for _, col := range cols {
		known[col.Name] = true
		if col.PrimaryKey {
			pk = append(pk, col.Name)
		}
	}
	if len(pk) == 0 && (len(ch.Deletes) > 0 || len(ch.Updates) > 0) {
		return nil, fmt.Errorf("%w: the table has no primary key, so its rows can't be told apart; change them from the console", ErrInvalid)
	}
	value := func(v *string) (string, error) {
		if v == nil {
			return "NULL", nil
		}
		if strings.ContainsRune(*v, 0) {
			return "", fmt.Errorf("%w: a value holds a NUL character", ErrInvalid)
		}
		return d.literal(*v), nil
	}
	writable := func(name string) error {
		if !known[name] {
			return fmt.Errorf("%w: unknown column %q", ErrInvalid, name)
		}
		if why, ok := d.locked[name]; ok {
			return fmt.Errorf("%w: column %s can't be edited here: %s", ErrInvalid, name, why)
		}
		return nil
	}
	// key finds a row by its primary key; label names it ("id 42").
	key := func(k map[string]string) (where []string, label string, err error) {
		if len(k) != len(pk) {
			return nil, "", fmt.Errorf("%w: a row is found by its primary key (%s)", ErrInvalid, strings.Join(pk, ", "))
		}
		names := make([]string, len(pk))
		for i, name := range pk {
			v, ok := k[name]
			if !ok {
				return nil, "", fmt.Errorf("%w: a row is found by its primary key (%s)", ErrInvalid, strings.Join(pk, ", "))
			}
			lit, err := value(&v)
			if err != nil {
				return nil, "", err
			}
			where = append(where, d.ident(name)+" = "+lit)
			names[i] = name + " " + v
		}
		return where, "row " + strings.Join(names, ", "), nil
	}

	var out []editStmt
	for _, del := range ch.Deletes {
		where, label, err := key(del.Key)
		if err != nil {
			return nil, err
		}
		cond := strings.Join(where, " AND ")
		out = append(out, editStmt{sql: fmt.Sprintf("DELETE FROM %s WHERE %s;", d.table, cond), where: cond, label: label})
	}
	for _, up := range ch.Updates {
		where, label, err := key(up.Key)
		if err != nil {
			return nil, err
		}
		if len(up.Set) == 0 {
			return nil, fmt.Errorf("%w: an update of %s sets no column", ErrInvalid, label)
		}
		for name := range up.Set {
			if err := writable(name); err != nil {
				return nil, err
			}
		}
		var set []string
		for _, col := range cols {
			v, ok := up.Set[col.Name]
			if !ok {
				continue
			}
			lit, err := value(v)
			if err != nil {
				return nil, err
			}
			was, ok := up.Was[col.Name]
			if !ok {
				return nil, fmt.Errorf("%w: an update of %s lacks %s's loaded value", ErrInvalid, label, col.Name)
			}
			set = append(set, d.ident(col.Name)+" = "+lit)
			if was == nil {
				where = append(where, d.ident(col.Name)+" IS NULL")
			} else if lit, err = value(was); err != nil {
				return nil, err
			} else {
				where = append(where, fmt.Sprintf(d.textEq, d.ident(col.Name), lit))
			}
		}
		cond := strings.Join(where, " AND ")
		out = append(out, editStmt{sql: fmt.Sprintf("UPDATE %s SET %s WHERE %s;", d.table, strings.Join(set, ", "), cond), where: cond, label: label})
	}
	for _, ins := range ch.Inserts {
		for name := range ins {
			if err := writable(name); err != nil {
				return nil, err
			}
		}
		var names, values []string
		for _, col := range cols {
			v, ok := ins[col.Name]
			if !ok {
				continue
			}
			lit, err := value(v)
			if err != nil {
				return nil, err
			}
			names = append(names, d.ident(col.Name))
			values = append(values, lit)
		}
		sql := fmt.Sprintf(d.noColumns, d.table)
		if len(names) > 0 {
			sql = fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s);", d.table, strings.Join(names, ", "), strings.Join(values, ", "))
		}
		out = append(out, editStmt{sql: sql})
	}
	return out, nil
}

func statementsOf(stmts []editStmt) ChangeResult {
	res := ChangeResult{Statements: make([]string, len(stmts))}
	for i, s := range stmts {
		res.Statements[i] = s.sql
	}
	return res
}

func (t dataTarget) pgApply(ctx context.Context, schema, table string, ch RowChanges, apply bool) (ChangeResult, error) {
	var kind string
	err := t.psqlJSON(ctx, fmt.Sprintf(`SELECT json_build_array(c.relkind) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = %s AND c.relname = %s;`, pgLiteral(schema), pgLiteral(table)), func(row []json.RawMessage) error {
		return unmarshalRow(row, &kind)
	})
	if err != nil {
		return ChangeResult{}, err
	}
	switch kind {
	case "":
		return ChangeResult{}, fmt.Errorf("table %s.%s: %w", schema, table, store.ErrNotFound)
	case "r", "p":
	default:
		return ChangeResult{}, fmt.Errorf("%w: %s.%s is a %s: only a table's rows can be edited", ErrInvalid, schema, table, pgRelKinds[kind])
	}
	cols, err := t.pgColumns(ctx, schema, table)
	if err != nil {
		return ChangeResult{}, err
	}
	stmts, err := editStatements(sqlEdit{
		table:     pgIdent(schema) + "." + pgIdent(table),
		ident:     pgIdent,
		literal:   pgLiteral,
		textEq:    "%s::text = %s",
		noColumns: "INSERT INTO %s DEFAULT VALUES;",
	}, cols, ch)
	if err != nil {
		return ChangeResult{}, err
	}
	res := statementsOf(stmts)
	if !apply {
		return res, nil
	}
	// One DO block: atomic, and FOUND tells whether a statement hit its row.
	// Column names win over PL/pgSQL's own variables (a column "found").
	tag := "$kp_" + randomHex(6) + "$"
	var b strings.Builder
	b.WriteString("DO " + tag + "\n#variable_conflict use_column\nBEGIN\n")
	for _, s := range stmts {
		b.WriteString(s.sql + "\n")
		if s.where != "" {
			fmt.Fprintf(&b, "IF NOT FOUND THEN RAISE EXCEPTION '%%', %s; END IF;\n", pgLiteral(rowChanged(s.label)))
		}
	}
	b.WriteString("END\n" + tag + ";\n")
	ctx, cancel := context.WithTimeout(ctx, dataConsoleTimeout)
	defer cancel()
	err = t.dk.Exec(ctx, t.container, docker.ExecOptions{
		Cmd:   []string{"psql", "-X", "-q", "-v", "ON_ERROR_STOP=1", "-U", t.svc.Env[pgUser], "-d", t.db, "-f", "-"},
		Env:   []string{fmt.Sprintf("PGOPTIONS=-c statement_timeout=%ds", int(dataConsoleTimeout.Seconds()))},
		Stdin: strings.NewReader(b.String()),
	})
	if err = clientError(err); err != nil {
		// The block's own context (CONTEXT: PL/pgSQL function ...) says nothing useful.
		msg, _, _ := strings.Cut(err.Error(), "\nCONTEXT:")
		return ChangeResult{}, fmt.Errorf("%w: %s", ErrInvalid, strings.TrimSpace(strings.TrimPrefix(msg, ErrInvalid.Error()+": ")))
	}
	res.Applied = true
	return res, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// mysqlQuote is a string literal for a session with NO_BACKSLASH_ESCAPES,
// which a save turns on: only the quote needs doubling, and the statements
// read as typed.
func mysqlQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// mysqlCheckRe finds which line of a save failed: a row check fails with
// "Subquery returns more than 1 row".
var mysqlCheckRe = regexp.MustCompile(`ERROR 1242 \(21000\) at line (\d+)`)

func (t dataTarget) mysqlApply(ctx context.Context, schema, table string, ch RowChanges, apply bool) (ChangeResult, error) {
	if schema != "" && schema != t.db {
		return ChangeResult{}, fmt.Errorf("table %s.%s: %w", schema, table, store.ErrNotFound)
	}
	var kind string
	err := t.mysqlJSON(ctx, fmt.Sprintf("SELECT JSON_ARRAY(TABLE_TYPE) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = %s",
		mysqlString(table)), func(row []json.RawMessage) error {
		return unmarshalRow(row, &kind)
	})
	if err != nil {
		return ChangeResult{}, err
	}
	if kind == "" {
		return ChangeResult{}, fmt.Errorf("table %s.%s: %w", t.db, table, store.ErrNotFound)
	}
	if k := mysqlTableKind(kind); k != "table" {
		return ChangeResult{}, fmt.Errorf("%w: %s is a %s: only a table's rows can be edited", ErrInvalid, table, k)
	}
	mcols, err := t.mysqlColumns(ctx, table)
	if err != nil {
		return ChangeResult{}, err
	}
	cols := make([]PgColumn, len(mcols))
	locked := map[string]string{}
	for i, col := range mcols {
		cols[i] = col.PgColumn
		if col.binary {
			locked[col.Name] = "it holds bytes, shown as hex; use the console"
		}
	}
	stmts, err := editStatements(sqlEdit{
		table:     mysqlIdent(table),
		ident:     mysqlIdent,
		literal:   mysqlQuote,
		textEq:    "CAST(%s AS CHAR) COLLATE utf8mb4_bin = %s",
		noColumns: "INSERT INTO %s () VALUES ();",
		locked:    locked,
	}, cols, ch)
	if err != nil {
		return ChangeResult{}, err
	}
	res := statementsOf(stmts)
	if !apply {
		return res, nil
	}
	// No anonymous blocks in MySQL: before each update or delete, a check
	// that fails ("Subquery returns more than 1 row") unless its row is
	// there. SET, not DO: MariaDB's DO turns errors into warnings. The
	// client stops at the first error and the transaction rolls back.
	var b strings.Builder
	b.WriteString("SET SESSION sql_mode = CONCAT_WS(',', NULLIF(@@SESSION.sql_mode, ''), 'NO_BACKSLASH_ESCAPES');\nSTART TRANSACTION;\n")
	checks := map[int]string{} // line of a check: its row
	for _, s := range stmts {
		if s.where != "" {
			checks[strings.Count(b.String(), "\n")+1] = s.label
			fmt.Fprintf(&b, "SET @kipitiny_check = (SELECT IF(COUNT(*) = 1, 0, (SELECT 1 UNION SELECT 2)) FROM %s WHERE %s);\n", mysqlIdent(table), s.where)
		}
		b.WriteString(s.sql + "\n")
	}
	b.WriteString("COMMIT;\n")
	ctx, cancel := context.WithTimeout(ctx, dataConsoleTimeout)
	defer cancel()
	err = t.dk.Exec(ctx, t.container, t.mysql(b.String(), true, dataConsoleTimeout, nil))
	if err != nil {
		var ee *docker.ExecError
		if errors.As(err, &ee) {
			if m := mysqlCheckRe.FindStringSubmatch(ee.Stderr); m != nil {
				if line, _ := strconv.Atoi(m[1]); checks[line] != "" {
					return ChangeResult{}, fmt.Errorf("%w: %s", ErrInvalid, rowChanged(checks[line]))
				}
			}
		}
		return ChangeResult{}, clientError(err)
	}
	res.Applied = true
	return res, nil
}
