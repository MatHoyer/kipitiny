package core

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// The MongoDB data browser runs mongosh scripts the manager writes; what
// callers give (database, collection, filter, sort) reaches them through
// the environment, never the script's text. Browsing and the read-only
// console run as the read-only user (readAnyDatabase).

// MongoDocs is one page of a collection's documents, each as relaxed
// Extended JSON.
type MongoDocs struct {
	Documents []string `json:"documents"`
	// Truncated lists the documents cut at DataCellMax.
	Truncated []int `json:"truncated"`
	HasMore   bool  `json:"hasMore"`
}

// DocQuery pages a collection. Filter and Sort are Extended JSON objects.
type DocQuery struct {
	Filter string `json:"filter,omitempty"`
	Sort   string `json:"sort,omitempty"`
	Limit  int    `json:"limit"`
	Skip   int    `json:"skip"`
}

// mongoBrowseScripts print one JSON line per result.
const (
	mongoDatabasesJS = `for (const d of db.adminCommand({ listDatabases: 1 }).databases) print(JSON.stringify([d.name, Number(d.sizeOnDisk)]));`

	mongoCollectionsJS = `const d = db.getSiblingDB(process.env.KP_DB);
for (const c of d.getCollectionInfos({}, { authorizedCollections: true })) {
  if (c.name.startsWith("system.")) continue;
  let n = -1, b = 0;
  if (c.type === "collection" || c.type === "timeseries") {
    try {
      const s = d.getCollection(c.name).aggregate([{ $collStats: { storageStats: {} } }]).next().storageStats;
      n = Number(s.count); b = Number(s.storageSize) + Number(s.totalIndexSize);
    } catch (e) {}
  }
  print(JSON.stringify([c.name, c.type, n, b]));
}`

	mongoFindJS = `const e = process.env;
const filter = EJSON.parse(e.KP_FILTER || "{}"), sort = EJSON.parse(e.KP_SORT || "{}");
db.getSiblingDB(e.KP_DB).getCollection(e.KP_COLL).find(filter).sort(sort).skip(Number(e.KP_SKIP)).limit(Number(e.KP_LIMIT))
  .maxTimeMS(Number(e.KP_MS)).forEach((doc) => print(JSON.stringify(EJSON.stringify(doc, { relaxed: true }))));`
)

// mongosh runs a script as the read-only user (or the service's own, the
// instance's root, when root is set), killed after timeout: mongosh would
// otherwise outlive a cancelled exec.
func (t dataTarget) mongosh(script string, root bool, timeout time.Duration, env ...string) docker.ExecOptions {
	auth := []string{"--username", readUser, "--password", readUserPassword(t.svc.Env[mongoPassword]), "--authenticationDatabase", "admin"}
	if root {
		auth = mongoAuthArgs(t.svc)
	}
	cmd := []string{"timeout", "-s", "KILL", strconv.Itoa(int(timeout.Seconds()) + 5), "mongosh", "--quiet", "--norc"}
	cmd = append(append(cmd, auth...), t.db, "--eval", script)
	return docker.ExecOptions{Cmd: cmd, Env: env}
}

// mongoJSON runs a browse script and hands each line, a JSON array, to fn.
func (t dataTarget) mongoJSON(ctx context.Context, script string, fn func([]json.RawMessage) error, env ...string) error {
	ctx, cancel := context.WithTimeout(ctx, dataBrowseTimeout+10*time.Second)
	defer cancel()
	return t.execLines(ctx, t.mongosh(script, false, dataBrowseTimeout, env...), func(line []byte) error {
		var row []json.RawMessage
		if err := json.Unmarshal(line, &row); err != nil {
			return fmt.Errorf("mongosh output: %w", err)
		}
		return fn(row)
	})
}

// mongoDatabases lists the instance's databases, the server's own aside.
// The service's own shows even before anything was written to it.
func (t dataTarget) mongoDatabases(ctx context.Context) ([]PgDatabase, error) {
	dbs := []PgDatabase{}
	main := t.svc.Env[mongoDatabase]
	err := t.mongoJSON(ctx, mongoDatabasesJS, func(row []json.RawMessage) error {
		var d PgDatabase
		if err := unmarshalRow(row, &d.Name, &d.Bytes); err != nil {
			return err
		}
		if !slices.Contains(mongoSkipped, d.Name) {
			d.Main = d.Name == main
			dbs = append(dbs, d)
		}
		return nil
	})
	if err == nil && !slices.ContainsFunc(dbs, func(d PgDatabase) bool { return d.Main }) {
		dbs = append(dbs, PgDatabase{Name: main, Main: true})
		slices.SortFunc(dbs, func(a, b PgDatabase) int { return strings.Compare(a.Name, b.Name) })
	}
	return dbs, err
}

// mongoCollections lists a database's collections and views as tables
// without columns.
func (t dataTarget) mongoCollections(ctx context.Context) ([]PgTable, error) {
	tables := []PgTable{}
	err := t.mongoJSON(ctx, mongoCollectionsJS, func(row []json.RawMessage) error {
		tb := PgTable{Schema: t.db, Columns: []PgColumn{}}
		if err := unmarshalRow(row, &tb.Name, &tb.Kind, &tb.RowEstimate, &tb.Bytes); err != nil {
			return err
		}
		tables = append(tables, tb)
		return nil
	}, "KP_DB="+t.db)
	slices.SortFunc(tables, func(a, b PgTable) int { return strings.Compare(a.Name, b.Name) })
	return tables, err
}

// mongoCollectionRe is what a new database's first collection may be
// called: plain, so it needs no quoting in mongosh.
var mongoCollectionRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,119}$`)

// mongoCreateDatabase creates a database by creating its first collection,
// as root. The name was checked like a PostgreSQL one.
func (t dataTarget) mongoCreateDatabase(ctx context.Context, name, collection string) error {
	if slices.Contains(mongoSkipped, name) {
		return fmt.Errorf("%w: %s is the server's own database", ErrInvalid, name)
	}
	if !mongoCollectionRe.MatchString(collection) || strings.HasPrefix(collection, "system.") {
		return fmt.Errorf("%w: a MongoDB database starts with a collection: letters, digits, _ . and -, starting with a letter or _", ErrInvalid)
	}
	dbs, err := t.mongoDatabases(ctx)
	if err != nil {
		return err
	}
	// The service's own is listed before it holds anything.
	if slices.ContainsFunc(dbs, func(d PgDatabase) bool { return d.Name == name && (d.Bytes > 0 || !d.Main) }) {
		return fmt.Errorf("%w: %s already has a database %s", ErrInvalid, t.svc.Name, name)
	}
	ctx, cancel := context.WithTimeout(ctx, dataConsoleTimeout)
	defer cancel()
	opts := t.mongosh(`db.getSiblingDB(process.env.KP_DB).createCollection(process.env.KP_COLL)`, true, dataConsoleTimeout)
	opts.Env = []string{"KP_DB=" + name, "KP_COLL=" + collection}
	return mongoError(clientError(t.dk.Exec(ctx, t.container, opts)))
}

// MongoDocuments returns one page of a collection's documents.
func (c *Core) MongoDocuments(ctx context.Context, id, database, collection string, q DocQuery) (MongoDocs, error) {
	if err := Require(ctx, store.ScopeRead); err != nil {
		return MongoDocs{}, err
	}
	t, err := c.databaseTarget(ctx, id, database)
	if err != nil {
		return MongoDocs{}, err
	}
	if t.svc.Kind != store.ServiceKindMongoDB {
		return MongoDocs{}, fmt.Errorf("%w: %s is not a mongodb database", ErrInvalid, t.svc.Name)
	}
	tables, err := t.mongoCollections(ctx)
	if err != nil {
		return MongoDocs{}, err
	}
	if !slices.ContainsFunc(tables, func(tb PgTable) bool { return tb.Name == collection }) {
		return MongoDocs{}, fmt.Errorf("collection %s.%s: %w", t.db, collection, store.ErrNotFound)
	}
	if q.Limit <= 0 || q.Limit > DataPageMax {
		q.Limit = DataPageMax
	}
	for name, v := range map[string]string{"filter": q.Filter, "sort": q.Sort} {
		if v = strings.TrimSpace(v); v != "" && (!strings.HasPrefix(v, "{") || len(v) > dataQueryMax) {
			return MongoDocs{}, fmt.Errorf("%w: the %s is a JSON object, like {\"field\": 1}", ErrInvalid, name)
		}
	}
	res := MongoDocs{Documents: []string{}, Truncated: []int{}}
	ctx, cancel := context.WithTimeout(ctx, dataBrowseTimeout+10*time.Second)
	defer cancel()
	err = t.execLines(ctx, t.mongosh(mongoFindJS, false, dataBrowseTimeout,
		"KP_DB="+t.db, "KP_COLL="+collection, "KP_FILTER="+q.Filter, "KP_SORT="+q.Sort,
		"KP_SKIP="+strconv.Itoa(max(q.Skip, 0)), "KP_LIMIT="+strconv.Itoa(q.Limit+1),
		"KP_MS="+strconv.FormatInt(dataBrowseTimeout.Milliseconds(), 10)), func(line []byte) error {
		if len(res.Documents) == q.Limit {
			res.HasMore = true
			return errStopLines
		}
		var doc string
		if err := json.Unmarshal(line, &doc); err != nil {
			return fmt.Errorf("mongosh output: %w", err)
		}
		doc, cut := truncate(doc)
		if cut {
			res.Truncated = append(res.Truncated, len(res.Documents))
		}
		res.Documents = append(res.Documents, doc)
		return nil
	})
	if err != nil {
		return MongoDocs{}, mongoError(err)
	}
	return res, nil
}

// mongoConsole runs JavaScript in mongosh against t.db, as the read-only
// user unless write is set; the reply is mongosh's own printout.
func (t dataTarget) mongoConsole(ctx context.Context, script string, write bool) (ConsoleResult, error) {
	out := &capWriter{max: dataOutputMax}
	opts := t.mongosh(script, write, dataConsoleTimeout)
	opts.Stdout = out
	if err := t.dk.Exec(ctx, t.container, opts); err != nil {
		err = mongoError(clientError(err))
		if !write && strings.Contains(err.Error(), "not authorized") {
			return ConsoleResult{}, fmt.Errorf("%w (read only: allow writes to change data)", err)
		}
		return ConsoleResult{}, err
	}
	text := strings.TrimRight(out.b.String(), "\n")
	if text == "" {
		text = "OK"
	}
	return ConsoleResult{Output: text, More: out.dropped}, nil
}

// mongoError keeps the first line of mongosh's error, its message (the
// stack trace follows).
func mongoError(err error) error {
	if err == nil {
		return nil
	}
	msg, _, _ := strings.Cut(err.Error(), "\n")
	if msg == err.Error() {
		return err
	}
	return fmt.Errorf("%w: %s", ErrInvalid, strings.TrimPrefix(msg, ErrInvalid.Error()+": "))
}
