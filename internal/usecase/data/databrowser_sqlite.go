package data

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/sqlitehelper"
)

// An app's SQLite databases are files in its volumes. Most images have no
// sqlite3, so the browser runs the manager's own `sqlite` command
// (internal/sqlitehelper) in a helper of the manager's image that mounts the
// volumes, kept like the file browser's. A database is its path,
// "<volume>/<path>"; its schema is "main". The shapes are PostgreSQL's
// (PgTable, PgRows, ConsoleResult), the SQL is built here as for the
// other kinds, names checked against the file first.

const (
	// sqliteSchema is the only schema the browser shows.
	sqliteSchema = "main"
	// sqliteBrowseBytes caps the cells of one page; sqliteReadMax what is
	// read of the helper's answer (JSON escapes may grow it).
	sqliteBrowseBytes = 8 << 20
	sqliteReadMax     = 64 << 20
)

// sqliteFile is the database a dataTarget of an app queries.
type sqliteFile struct {
	c   *Data
	vol fileVolume
	abs string // in the helper
}

// startSQLiteHelper runs an idle helper of the manager's own image, its
// `sqlite` command run in it over exec.
func (c *Data) startSQLiteHelper(ctx context.Context, dk *docker.Client, name string, mounts []mount.Mount) (string, error) {
	image := c.mgr.OwnImageRef()
	// The manager's public image: no registry credential.
	if err := dk.EnsureImage(ctx, image, "", ""); err != nil {
		return "", fmt.Errorf("pull %s: %w", image, err)
	}
	return dk.RunMoby(ctx, client.ContainerCreateOptions{
		Name: name,
		Config: &container.Config{
			Image:  image,
			Cmd:    []string{"sqlite", "idle"},
			Labels: map[string]string{domain.LabelManaged: "true", domain.LabelComponent: volumeHelperComponent},
			// SQLite's temporary files (big sorts) go to the tmpfs.
			Env: []string{"SQLITE_TMPDIR=/tmp"},
			// The image's own checks the manager's port.
			Healthcheck: &container.HealthConfig{Test: []string{"NONE"}},
		},
		HostConfig: &container.HostConfig{
			NetworkMode:    "none",
			Mounts:         mounts,
			ReadonlyRootfs: true,
			Tmpfs:          map[string]string{"/tmp": ""},
			SecurityOpt:    []string{"no-new-privileges"},
			LogConfig:      docker.DefaultLogConfig(),
		},
	})
}

// sqliteExec runs one request of the helper in svc's SQLite helper and
// decodes its answer; an error it reports is the caller's.
func (c *Data) sqliteExec(ctx context.Context, svc domain.Service, req sqlitehelper.Request) (sqlitehelper.Response, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return sqlitehelper.Response{}, err
	}
	// Read whole, decoded after: an exec that failed (a helper gone, to
	// start again) then reports its own error, not an empty answer.
	var out []byte
	err = c.helperExec(ctx, svc, true, docker.ExecOptions{Cmd: []string{"/kipitiny", "sqlite"}, Stdin: bytes.NewReader(b)}, func(r io.Reader) error {
		out, err = io.ReadAll(io.LimitReader(r, sqliteReadMax))
		return err
	})
	if err != nil {
		return sqlitehelper.Response{}, clientError(err)
	}
	var res sqlitehelper.Response
	if err := json.Unmarshal(out, &res); err != nil {
		return sqlitehelper.Response{}, fmt.Errorf("sqlite helper output: %w", err)
	}
	if res.Error != "" && res.Failed == 0 {
		return res, fmt.Errorf("%w: %s", ErrInvalid, res.Error)
	}
	return res, nil
}

// sqliteDatabases lists the SQLite files in svc's volumes, as
// "<volume>/<path>".
func (c *Data) sqliteDatabases(ctx context.Context, svc domain.Service) ([]PgDatabase, error) {
	vols := fileVolumes(svc)
	roots := make([]string, len(vols))
	for i, v := range vols {
		roots[i] = v.root()
	}
	ctx, cancel := context.WithTimeout(ctx, filesBrowseTimeout)
	defer cancel()
	res, err := c.sqliteExec(ctx, svc, sqlitehelper.Request{Op: sqlitehelper.OpFind, Roots: roots, TimeoutMS: int((filesBrowseTimeout - 5*time.Second).Milliseconds())})
	if err != nil {
		return nil, err
	}
	dbs := []PgDatabase{}
	for _, f := range res.Files {
		for _, v := range vols {
			if strings.HasPrefix(f.Path, v.root()+"/") {
				dbs = append(dbs, PgDatabase{Name: v.display(f.Path), Bytes: f.Size})
				break
			}
		}
	}
	return dbs, nil
}

// sqliteTarget resolves one of svc's SQLite files (the first found when
// database is empty). The helper checks it is a SQLite file inside its
// volume.
func (c *Data) sqliteTarget(ctx context.Context, svc domain.Service, database string) (dataTarget, error) {
	if database == "" {
		dbs, err := c.sqliteDatabases(ctx, svc)
		if err != nil {
			return dataTarget{}, err
		}
		if len(dbs) == 0 {
			return dataTarget{}, fmt.Errorf("%w: no SQLite database found in the volumes of %s", ErrInvalid, svc.Name)
		}
		database = dbs[0].Name
	}
	vol, abs, err := volumePath(svc, database)
	if err != nil {
		return dataTarget{}, err
	}
	if vol.container {
		return dataTarget{}, fmt.Errorf("%w: only a database in a volume can be browsed; one in the container's own files is lost on the next deploy: keep it in a volume", ErrInvalid)
	}
	if abs == "" || abs == vol.root() {
		return dataTarget{}, fmt.Errorf("%w: %q is not a database file: use <volume>/<path>", ErrInvalid, database)
	}
	return dataTarget{svc: svc, db: database, sqlite: &sqliteFile{c: c, vol: vol, abs: abs}}, nil
}

// sqliteRun runs a request on the target's file.
func (t dataTarget) sqliteRun(ctx context.Context, req sqlitehelper.Request) (sqlitehelper.Response, error) {
	req.Root, req.File = t.sqlite.vol.root(), t.sqlite.abs
	if req.TimeoutMS == 0 {
		req.TimeoutMS = int(dataBrowseTimeout.Milliseconds())
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(req.TimeoutMS)*time.Millisecond+5*time.Second)
	defer cancel()
	return t.sqlite.c.sqliteExec(ctx, t.svc, req)
}

// sqliteRowsOf runs a read-only query the manager built and returns all its
// cells as text (names, types: nothing long).
func (t dataTarget) sqliteRowsOf(ctx context.Context, sql string) ([][]*string, error) {
	res, err := t.sqliteRun(ctx, sqlitehelper.Request{Op: sqlitehelper.OpQuery, SQL: sql, Cell: DataCellMax})
	return res.Rows, err
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// sqliteUserTables filters what the browser lists: SQLite's own tables aside.
const sqliteUserTables = `s.type IN ('table', 'view') AND s.name NOT LIKE 'sqlite\_%' ESCAPE '\'`

func (t dataTarget) sqliteTables(ctx context.Context) ([]PgTable, error) {
	rows, err := t.sqliteRowsOf(ctx, `SELECT 't', s.name, s.type, NULL, NULL, NULL, NULL FROM sqlite_schema s WHERE `+sqliteUserTables+`
UNION ALL
SELECT 'c', s.name, p.name, p.type, p."notnull", p.pk, p.cid FROM sqlite_schema s, pragma_table_info(s.name) p WHERE `+sqliteUserTables+`
UNION ALL
SELECT 'f', s.name, f."from", f."table", f."to", NULL, f.id FROM sqlite_schema s, pragma_foreign_key_list(s.name) f
WHERE `+sqliteUserTables+` AND (SELECT count(*) FROM pragma_foreign_key_list(s.name) g WHERE g.id = f.id) = 1
ORDER BY 2, 7`)
	if err != nil {
		return nil, err
	}
	tables := []PgTable{}
	byName := map[string]int{}
	for _, r := range rows {
		if str(r[0]) == "t" {
			byName[str(r[1])] = len(tables)
			tables = append(tables, PgTable{Schema: sqliteSchema, Name: str(r[1]), Kind: str(r[2]), RowEstimate: -1, Columns: []PgColumn{}})
		}
	}
	// Columns, then foreign keys: the rows of both come mixed.
	for _, tag := range []string{"c", "f"} {
		for _, r := range rows {
			i, ok := byName[str(r[1])]
			if !ok || str(r[0]) != tag {
				continue
			}
			if tag == "c" {
				tables[i].Columns = append(tables[i].Columns, PgColumn{Name: str(r[2]), Type: str(r[3]), Nullable: str(r[4]) == "0", PrimaryKey: str(r[5]) != "0"})
				continue
			}
			// The parent's key when the column isn't named, resolved below.
			ref := &PgRef{Schema: sqliteSchema, Table: str(r[3]), Column: str(r[4])}
			for j := range tables[i].Columns {
				if c := &tables[i].Columns[j]; c.Name == str(r[2]) && c.References == nil {
					c.References = ref
				}
			}
		}
	}
	for i := range tables {
		for j := range tables[i].Columns {
			ref := tables[i].Columns[j].References
			if ref == nil || ref.Column != "" {
				continue
			}
			if p, ok := byName[ref.Table]; ok {
				if pk := primaryKey(tables[p].Columns); len(pk) == 1 {
					ref.Column = pk[0]
					continue
				}
			}
			tables[i].Columns[j].References = nil
		}
	}
	return tables, nil
}

func primaryKey(cols []PgColumn) []string {
	var pk []string
	for _, c := range cols {
		if c.PrimaryKey {
			pk = append(pk, c.Name)
		}
	}
	return pk
}

// sqliteColumns returns a table's or view's columns and its kind.
func (t dataTarget) sqliteColumns(ctx context.Context, schema, table string) ([]PgColumn, string, error) {
	if schema != "" && schema != sqliteSchema {
		return nil, "", fmt.Errorf("table %s.%s: %w", schema, table, domain.ErrNotFound)
	}
	lit := pgLiteral(table)
	rows, err := t.sqliteRowsOf(ctx, `SELECT s.type, p.name, p.type, p."notnull", p.pk FROM sqlite_schema s, pragma_table_info(s.name) p
WHERE `+sqliteUserTables+` AND s.name = `+lit+` ORDER BY p.cid`)
	if err != nil {
		return nil, "", err
	}
	if len(rows) == 0 {
		return nil, "", fmt.Errorf("table %s.%s: %w", sqliteSchema, table, domain.ErrNotFound)
	}
	cols := make([]PgColumn, len(rows))
	for i, r := range rows {
		cols[i] = PgColumn{Name: str(r[1]), Type: str(r[2]), Nullable: str(r[3]) == "0", PrimaryKey: str(r[4]) != "0"}
	}
	return cols, str(rows[0][0]), nil
}

var sqliteFilterOps = map[string]string{
	"=":       "%s = %s",
	"!=":      "%s <> %s",
	"<":       "%s < %s",
	"<=":      "%s <= %s",
	">":       "%s > %s",
	">=":      "%s >= %s",
	"like":    `CAST(%s AS TEXT) LIKE %s ESCAPE '\'`,
	"null":    "%s IS NULL",
	"notnull": "%s IS NOT NULL",
}

// sqliteSelect builds the query for q, like pgSelect: every name it quotes
// was checked against cols, values are literals. A page reads each cell as
// text (blobs as 0x hex), cut a byte past what's shown, and one row past
// q.Limit; an export reads cells whole.
func sqliteSelect(table string, cols []PgColumn, q RowQuery, page bool) (string, error) {
	known := make(map[string]bool, len(cols))
	exprs := make([]string, len(cols))
	var pk []string
	for i, col := range cols {
		known[col.Name] = true
		id := pgIdent(col.Name)
		exprs[i] = id
		if page {
			exprs[i] = fmt.Sprintf("CASE typeof(%[1]s) WHEN 'blob' THEN '0x' || lower(hex(substr(%[1]s, 1, %[2]d))) ELSE substr(CAST(%[1]s AS TEXT), 1, %[3]d) END",
				id, DataCellMax/2, DataCellMax+1)
		}
		if col.PrimaryKey {
			pk = append(pk, id)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "SELECT %s FROM %s", strings.Join(exprs, ", "), pgIdent(table))
	var where []string
	for _, f := range q.Filters {
		if !known[f.Column] {
			return "", fmt.Errorf("%w: unknown column %q", ErrInvalid, f.Column)
		}
		op, ok := sqliteFilterOps[f.Op]
		if !ok {
			return "", fmt.Errorf("%w: unknown filter operator %q", ErrInvalid, f.Op)
		}
		if strings.ContainsRune(f.Value, 0) {
			return "", fmt.Errorf("%w: a value holds a NUL character", ErrInvalid)
		}
		if strings.Count(op, "%s") == 1 {
			where = append(where, fmt.Sprintf(op, pgIdent(f.Column)))
		} else {
			where = append(where, fmt.Sprintf(op, pgIdent(f.Column), pgLiteral(f.Value)))
		}
	}
	if q.Search != "" {
		if strings.ContainsRune(q.Search, 0) {
			return "", fmt.Errorf("%w: the search holds a NUL character", ErrInvalid)
		}
		// Matched literally; SQLite's LIKE ignores ASCII case already.
		pattern := pgLiteral("%" + likeEscaper.Replace(q.Search) + "%")
		any := make([]string, len(cols))
		for i, col := range cols {
			any[i] = fmt.Sprintf(`CAST(%s AS TEXT) LIKE %s ESCAPE '\'`, pgIdent(col.Name), pattern)
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
	return b.String(), nil
}

func (t dataTarget) sqliteRows(ctx context.Context, schema, table string, q RowQuery) (PgRows, error) {
	cols, _, err := t.sqliteColumns(ctx, schema, table)
	if err != nil {
		return PgRows{}, err
	}
	if q.Limit <= 0 || q.Limit > DataPageMax {
		q.Limit = DataPageMax
	}
	sql, err := sqliteSelect(table, cols, q, true)
	if err != nil {
		return PgRows{}, err
	}
	res, err := t.sqliteRun(ctx, sqlitehelper.Request{Op: sqlitehelper.OpQuery, SQL: sql, Rows: q.Limit, Cell: DataCellMax, Bytes: sqliteBrowseBytes})
	if err != nil {
		return PgRows{}, err
	}
	out := PgRows{Columns: cols, Rows: res.Rows, Truncated: res.Truncated, HasMore: res.More}
	if out.Rows == nil {
		out.Rows = [][]*string{}
	}
	if out.Truncated == nil {
		out.Truncated = [][2]int{}
	}
	return out, nil
}

func (t dataTarget) sqliteExport(ctx context.Context, schema, table string, q RowQuery, w io.Writer) error {
	cols, _, err := t.sqliteColumns(ctx, schema, table)
	if err != nil {
		return err
	}
	sql, err := sqliteSelect(table, cols, q, false)
	if err != nil {
		return err
	}
	req := sqlitehelper.Request{Op: sqlitehelper.OpCSV, SQL: sql, Root: t.sqlite.vol.root(), File: t.sqlite.abs, TimeoutMS: int(dataExportTimeout.Milliseconds())}
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, dataExportTimeout+5*time.Second)
	defer cancel()
	err = t.sqlite.c.helperExec(ctx, t.svc, true, docker.ExecOptions{Cmd: []string{"/kipitiny", "sqlite"}, Stdin: bytes.NewReader(b)}, func(r io.Reader) error {
		_, err := io.Copy(w, r)
		return err
	})
	return clientError(err)
}

// sqliteConsole runs sql, read-only unless write (then in one
// transaction); the last statement's rows show.
func (t dataTarget) sqliteConsole(ctx context.Context, sql string, write bool) (ConsoleResult, error) {
	res, err := t.sqliteRun(ctx, sqlitehelper.Request{
		Op: sqlitehelper.OpQuery, SQL: sql, Write: write,
		Rows: DataPageMax, Cell: DataCellMax, Bytes: dataOutputMax, TimeoutMS: int(dataConsoleTimeout.Milliseconds()),
	})
	if err != nil {
		if !write && strings.Contains(err.Error(), "readonly database") {
			return ConsoleResult{}, fmt.Errorf("%w (read only: allow writes to change data)", err)
		}
		return ConsoleResult{}, err
	}
	switch {
	case res.Columns == nil && res.Changes > 0:
		return ConsoleResult{Output: fmt.Sprintf("OK, %d rows changed", res.Changes)}, nil
	case res.Columns == nil:
		return ConsoleResult{Output: "OK"}, nil
	case len(res.Rows) == 0:
		return ConsoleResult{Output: "0 rows"}, nil
	}
	return ConsoleResult{Columns: res.Columns, Rows: res.Rows, Truncated: res.Truncated, More: res.More}, nil
}

// sqliteBlob reports a declared type SQLite gives BLOB affinity by name.
func sqliteBlob(typ string) bool {
	return strings.Contains(strings.ToUpper(typ), "BLOB")
}

func (t dataTarget) sqliteApply(ctx context.Context, schema, table string, ch RowChanges, apply bool) (ChangeResult, error) {
	cols, kind, err := t.sqliteColumns(ctx, schema, table)
	if err != nil {
		return ChangeResult{}, err
	}
	if kind != "table" {
		return ChangeResult{}, fmt.Errorf("%w: %s is a %s: only a table's rows can be edited", ErrInvalid, table, kind)
	}
	locked := map[string]string{}
	for _, col := range cols {
		if sqliteBlob(col.Type) {
			locked[col.Name] = "it holds bytes, shown as hex; use the console"
		}
	}
	stmts, err := editStatements(sqlEdit{
		table:     pgIdent(table),
		ident:     pgIdent,
		literal:   pgLiteral,
		textEq:    "CAST(%s AS TEXT) = %s",
		noColumns: "INSERT INTO %s DEFAULT VALUES;",
		locked:    locked,
	}, cols, ch)
	if err != nil {
		return ChangeResult{}, err
	}
	res := statementsOf(stmts)
	if !apply {
		return res, nil
	}
	run := make([]sqlitehelper.Statement, len(stmts))
	for i, s := range stmts {
		run[i] = sqlitehelper.Statement{SQL: s.sql, One: s.where != ""}
	}
	out, err := t.sqliteRun(ctx, sqlitehelper.Request{Op: sqlitehelper.OpApply, Write: true, Statements: run, TimeoutMS: int(dataConsoleTimeout.Milliseconds())})
	if err != nil {
		return ChangeResult{}, err
	}
	if out.Failed > 0 && out.Failed <= len(stmts) {
		return ChangeResult{}, fmt.Errorf("%w: %s", ErrInvalid, rowChanged(stmts[out.Failed-1].label))
	}
	res.Applied = true
	return res, nil
}

// sqliteDDL runs one statement read-write.
func (t dataTarget) sqliteDDL(ctx context.Context, sql string) error {
	_, err := t.sqliteRun(ctx, sqlitehelper.Request{Op: sqlitehelper.OpApply, Write: true,
		Statements: []sqlitehelper.Statement{{SQL: sql}}, TimeoutMS: int(dataConsoleTimeout.Milliseconds())})
	return err
}
