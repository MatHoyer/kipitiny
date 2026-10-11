package core

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// Creating and dropping tables and collections from the data browser. Like
// row edits, the manager writes the statement, a preview returns it, and
// only apply runs it.

// NewColumn is a column of a table being created. Type is the database's
// own (bigserial, varchar(255), timestamp with time zone...); Default is
// an SQL expression, empty for none.
type NewColumn struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Nullable   bool   `json:"nullable"`
	PrimaryKey bool   `json:"primaryKey"`
	Default    string `json:"default,omitempty"`
}

// sqlTypeRe is what a column type may look like: words, sizes, arrays. No
// quotes or statement separators, so it can't end the statement.
var sqlTypeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_ ,()\[\]]{0,99}$`)

// sqlDefaultBad finds what a default expression may not hold: another
// statement or a comment hiding the rest of the line.
var sqlDefaultBad = regexp.MustCompile(`;|--|/\*`)

// schemaAudit records a create or drop once applied.
func (c *Core) schemaAudit(ctx context.Context, id string, apply bool, err *error) {
	if !apply {
		return
	}
	status := http.StatusOK
	if *err != nil {
		status = http.StatusBadRequest
	}
	c.Audit(ctx, "data schema", id, status, *err)
}

// CreateTable creates a table in a PostgreSQL, MySQL or MariaDB database
// (schema is PostgreSQL's; MySQL's is the database), or with apply unset
// only returns the statement. Admin only.
func (c *Core) CreateTable(ctx context.Context, id, database, schema, table string, cols []NewColumn, apply bool) (res ChangeResult, err error) {
	if err := Require(ctx, store.ScopeAdmin); err != nil {
		return ChangeResult{}, err
	}
	defer c.schemaAudit(ctx, id, apply, &err)
	t, err := c.sqlTarget(ctx, id, database)
	if err != nil {
		return ChangeResult{}, err
	}
	mysql := t.svc.Kind.IsMySQL()
	ident, name := pgIdent, ""
	if mysql {
		ident = mysqlIdent
	}
	if table = strings.TrimSpace(table); table == "" || len(table) > 63 {
		return ChangeResult{}, fmt.Errorf("%w: a table name has 1 to 63 characters", ErrInvalid)
	}
	if mysql {
		name = ident(table)
	} else {
		if schema == "" {
			schema = "public"
		}
		name = ident(schema) + "." + ident(table)
	}
	sql, err := createTableSQL(name, ident, cols)
	if err != nil {
		return ChangeResult{}, err
	}
	res = ChangeResult{Statements: []string{sql}}
	if !apply {
		return res, nil
	}
	if err := t.runDDL(ctx, sql); err != nil {
		return ChangeResult{}, err
	}
	res.Applied = true
	return res, nil
}

func createTableSQL(name string, ident func(string) string, cols []NewColumn) (string, error) {
	if len(cols) == 0 {
		return "", fmt.Errorf("%w: a table needs at least one column", ErrInvalid)
	}
	var lines, pk []string
	seen := map[string]bool{}
	for _, col := range cols {
		col.Name, col.Type, col.Default = strings.TrimSpace(col.Name), strings.TrimSpace(col.Type), strings.TrimSpace(col.Default)
		switch {
		case col.Name == "" || len(col.Name) > 63:
			return "", fmt.Errorf("%w: a column name has 1 to 63 characters", ErrInvalid)
		case seen[col.Name]:
			return "", fmt.Errorf("%w: two columns are named %s", ErrInvalid, col.Name)
		case !sqlTypeRe.MatchString(col.Type):
			return "", fmt.Errorf("%w: column %s: %q is not a type", ErrInvalid, col.Name, col.Type)
		case sqlDefaultBad.MatchString(col.Default):
			return "", fmt.Errorf("%w: column %s: a default is one expression, without ; or comments", ErrInvalid, col.Name)
		}
		seen[col.Name] = true
		line := "  " + ident(col.Name) + " " + col.Type
		if !col.Nullable && !col.PrimaryKey {
			line += " NOT NULL"
		}
		if col.Default != "" {
			line += " DEFAULT " + col.Default
		}
		if col.PrimaryKey {
			pk = append(pk, ident(col.Name))
		}
		lines = append(lines, line)
	}
	if len(pk) > 0 {
		lines = append(lines, "  PRIMARY KEY ("+strings.Join(pk, ", ")+")")
	}
	return "CREATE TABLE " + name + " (\n" + strings.Join(lines, ",\n") + "\n);", nil
}

// DropTable drops a table or view of a PostgreSQL, MySQL or MariaDB
// database, or with apply unset only returns the statement. Nothing that
// depends on it is dropped with it: the database refuses instead. Admin
// only.
func (c *Core) DropTable(ctx context.Context, id, database, schema, table string, apply bool) (res ChangeResult, err error) {
	if err := Require(ctx, store.ScopeAdmin); err != nil {
		return ChangeResult{}, err
	}
	defer c.schemaAudit(ctx, id, apply, &err)
	t, err := c.sqlTarget(ctx, id, database)
	if err != nil {
		return ChangeResult{}, err
	}
	tables, err := c.PgTables(ctx, id, database)
	if err != nil {
		return ChangeResult{}, err
	}
	mysql := t.svc.Kind.IsMySQL()
	i := slices.IndexFunc(tables, func(tb PgTable) bool { return tb.Name == table && (mysql || tb.Schema == schema) })
	if i < 0 {
		return ChangeResult{}, fmt.Errorf("table %s.%s: %w", schema, table, store.ErrNotFound)
	}
	what := map[string]string{"view": "VIEW", "materialized view": "MATERIALIZED VIEW", "foreign table": "FOREIGN TABLE"}[tables[i].Kind]
	if what == "" {
		what = "TABLE"
	}
	sql := "DROP " + what + " " + pgIdent(tables[i].Schema) + "." + pgIdent(table) + ";"
	if mysql {
		sql = "DROP " + what + " " + mysqlIdent(table) + ";"
	}
	res = ChangeResult{Statements: []string{sql}}
	if !apply {
		return res, nil
	}
	if err := t.runDDL(ctx, sql); err != nil {
		return ChangeResult{}, err
	}
	res.Applied = true
	return res, nil
}

// runDDL runs one statement as the service's user (root on MySQL and
// MariaDB), with the console's timeout.
func (t dataTarget) runDDL(ctx context.Context, sql string) error {
	ctx, cancel := context.WithTimeout(ctx, dataConsoleTimeout)
	defer cancel()
	if t.svc.Kind.IsMySQL() {
		return clientError(t.dk.Exec(ctx, t.container, t.mysql(sql, true, dataConsoleTimeout, nil)))
	}
	return clientError(t.dk.Exec(ctx, t.container, docker.ExecOptions{
		Cmd:   []string{"psql", "-X", "-q", "-v", "ON_ERROR_STOP=1", "-U", t.svc.Env[pgUser], "-d", t.db, "-f", "-"},
		Env:   []string{fmt.Sprintf("PGOPTIONS=-c statement_timeout=%ds", int(dataConsoleTimeout.Seconds()))},
		Stdin: strings.NewReader(sql),
	}))
}

// CreateCollection creates a collection in a MongoDB database, or with
// apply unset only returns the call. Admin only.
func (c *Core) CreateCollection(ctx context.Context, id, database, collection string, apply bool) (res ChangeResult, err error) {
	if err := Require(ctx, store.ScopeAdmin); err != nil {
		return ChangeResult{}, err
	}
	defer c.schemaAudit(ctx, id, apply, &err)
	t, err := c.mongoTarget(ctx, id, database)
	if err != nil {
		return ChangeResult{}, err
	}
	if !mongoCollectionRe.MatchString(collection) || strings.HasPrefix(collection, "system.") {
		return ChangeResult{}, fmt.Errorf("%w: a collection name has letters, digits, _ . and -, starting with a letter or _", ErrInvalid)
	}
	// MongoDB answers ok to creating one that exists.
	tables, err := t.mongoCollections(ctx)
	if err != nil {
		return ChangeResult{}, err
	}
	if slices.ContainsFunc(tables, func(tb PgTable) bool { return tb.Name == collection }) {
		return ChangeResult{}, fmt.Errorf("%w: %s already has a collection %s", ErrInvalid, t.db, collection)
	}
	res = ChangeResult{Statements: []string{"db.createCollection(" + jsonString(collection) + ")"}}
	if !apply {
		return res, nil
	}
	if err := t.mongoRun(ctx, `db.getSiblingDB(process.env.KP_DB).createCollection(process.env.KP_COLL)`, collection); err != nil {
		return ChangeResult{}, err
	}
	res.Applied = true
	return res, nil
}

// DropCollection drops a MongoDB collection or view with its documents
// and indexes, or with apply unset only returns the call. Admin only.
func (c *Core) DropCollection(ctx context.Context, id, database, collection string, apply bool) (res ChangeResult, err error) {
	if err := Require(ctx, store.ScopeAdmin); err != nil {
		return ChangeResult{}, err
	}
	defer c.schemaAudit(ctx, id, apply, &err)
	t, err := c.mongoTarget(ctx, id, database)
	if err != nil {
		return ChangeResult{}, err
	}
	tables, err := t.mongoCollections(ctx)
	if err != nil {
		return ChangeResult{}, err
	}
	if !slices.ContainsFunc(tables, func(tb PgTable) bool { return tb.Name == collection }) {
		return ChangeResult{}, fmt.Errorf("collection %s.%s: %w", t.db, collection, store.ErrNotFound)
	}
	coll := "db.getCollection(" + jsonString(collection) + ")"
	if mongoIdentRe.MatchString(collection) {
		coll = "db." + collection
	}
	res = ChangeResult{Statements: []string{coll + ".drop()"}}
	if !apply {
		return res, nil
	}
	if err := t.mongoRun(ctx, `if (!db.getSiblingDB(process.env.KP_DB).getCollection(process.env.KP_COLL).drop()) throw new Error("the collection was not dropped")`, collection); err != nil {
		return ChangeResult{}, err
	}
	res.Applied = true
	return res, nil
}

// mongoTarget is databaseTarget for MongoDB only.
func (c *Core) mongoTarget(ctx context.Context, id, database string) (dataTarget, error) {
	t, err := c.databaseTarget(ctx, id, database)
	if err == nil && t.svc.Kind != store.ServiceKindMongoDB {
		err = fmt.Errorf("%w: %s is not a mongodb database", ErrInvalid, t.svc.Name)
	}
	return t, err
}

// mongoRun runs a script as root against t.db, the collection in KP_COLL.
func (t dataTarget) mongoRun(ctx context.Context, script, collection string) error {
	ctx, cancel := context.WithTimeout(ctx, dataConsoleTimeout+10*time.Second)
	defer cancel()
	err := mongoError(clientError(t.dk.Exec(ctx, t.container, t.mongosh(script, true, dataConsoleTimeout, "KP_DB="+t.db, "KP_COLL="+collection))))
	if err != nil {
		return fmt.Errorf("%w: %s", ErrInvalid, mongoshMessage(err.Error()))
	}
	return nil
}
