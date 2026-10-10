package core

import (
	"errors"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// KIPITINY_TEST_DOCKER=1 go test ./internal/core/ -run DataBrowserDocker
func TestDataBrowserDockerMySQL(t *testing.T) {
	for _, kind := range []store.ServiceKind{store.ServiceKindMySQL, store.ServiceKindMariaDB} {
		t.Run(string(kind), func(t *testing.T) {
			c, ctx, svc, exec := dumpTestDB(t, kind)
			ctx = WithActor(ctx, Actor{Kind: "user", Name: "test", Scope: store.ScopeAdmin})
			client := mysqlBin(kind, "mysql")
			exec(`MYSQL_PWD="$MYSQL_PASSWORD" ` + client + ` -h 127.0.0.1 -u"$MYSQL_USER" "$MYSQL_DATABASE" -e "
CREATE TABLE items (id INT PRIMARY KEY, name TEXT, b BLOB, n INT NULL);
INSERT INTO items VALUES (1, 'multi\nline', 0xdead, NULL), (2, REPEAT('x', 5000), NULL, 5), (3, 'it''s', NULL, 7);
CREATE VIEW v AS SELECT id FROM items;"`)

			dbs, err := c.PgDatabases(ctx, svc.ID)
			if err != nil || len(dbs) != 1 || dbs[0].Name != "app" || !dbs[0].Main {
				t.Fatalf("databases = %+v, %v", dbs, err)
			}
			tables, err := c.PgTables(ctx, svc.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(tables) != 2 || tables[0].Name != "items" || tables[0].Kind != "table" || tables[0].Schema != "app" ||
				len(tables[0].Columns) != 4 || !tables[0].Columns[0].PrimaryKey || !tables[0].Columns[3].Nullable || tables[1].Kind != "view" {
				t.Fatalf("tables = %+v", tables)
			}

			rows, err := c.PgRows(ctx, svc.ID, "", "app", "items", RowQuery{Limit: 2})
			if err != nil {
				t.Fatal(err)
			}
			if !rows.HasMore || len(rows.Rows) != 2 || *rows.Rows[0][1] != "multi\nline" || *rows.Rows[0][2] != "0xDEAD" ||
				rows.Rows[0][3] != nil || len(*rows.Rows[1][1]) != DataCellMax || len(rows.Truncated) != 1 {
				t.Fatalf("rows = %+v", rows)
			}
			rows, err = c.PgRows(ctx, svc.ID, "", "", "items", RowQuery{Search: "it's", Filters: []PgFilter{{Column: "n", Op: ">", Value: "6"}}, OrderBy: "id", Desc: true})
			if err != nil || len(rows.Rows) != 1 || *rows.Rows[0][0] != "3" {
				t.Fatalf("filtered = %+v, %v", rows, err)
			}
			if _, err := c.PgRows(ctx, svc.ID, "", "", "items", RowQuery{OrderBy: "nope"}); !errors.Is(err, ErrInvalid) {
				t.Errorf("unknown column: %v", err)
			}
			if _, err := c.PgRows(ctx, svc.ID, "", "", "missing", RowQuery{}); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("missing table: %v", err)
			}

			var csv strings.Builder
			if err := c.PgExport(ctx, svc.ID, "", "app", "items", RowQuery{OrderBy: "id"}, &csv); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(csv.String(), "id,name,b,n\n1,\"multi\nline\",0xDEAD,\n") || !strings.Contains(csv.String(), ","+strings.Repeat("x", 5000)+",") {
				t.Fatalf("csv = %.200q", csv.String())
			}

			res, err := c.DataConsole(ctx, svc.ID, "", "SELECT id, n FROM items ORDER BY id", false)
			if err != nil || len(res.Columns) != 2 || len(res.Rows) != 3 || res.Rows[0][1] != nil || *res.Rows[1][1] != "5" {
				t.Fatalf("console = %+v, %v", res, err)
			}
			if _, err := c.DataConsole(ctx, svc.ID, "", "DELETE FROM items", false); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "allow writes") {
				t.Errorf("read-only delete: %v", err)
			}
			if _, err := c.DataConsole(ctx, svc.ID, "", "SELEC 1", false); !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), "ERROR 1064") {
				t.Errorf("syntax error: %v", err)
			}
			res, err = c.DataConsole(ctx, svc.ID, "", "UPDATE items SET n = 1 WHERE id = 1", true)
			if err != nil || res.Output != "OK" {
				t.Fatalf("write = %+v, %v", res, err)
			}

			if _, err := c.CreatePgDatabase(ctx, svc.ID, "second"); err != nil {
				t.Fatal(err)
			}
			// The app user owns it.
			exec(`MYSQL_PWD="$MYSQL_PASSWORD" ` + client + ` -h 127.0.0.1 -u"$MYSQL_USER" second -e "CREATE TABLE t (x INT); INSERT INTO t VALUES (1)"`)
			if tables, err := c.PgTables(ctx, svc.ID, "second"); err != nil || len(tables) != 1 || tables[0].Schema != "second" {
				t.Fatalf("second tables = %+v, %v", tables, err)
			}
			if _, err := c.PgTables(ctx, svc.ID, "mysql"); !errors.Is(err, ErrInvalid) {
				t.Errorf("system database browsable: %v", err)
			}
		})
	}
}

func TestDataBrowserDockerMongo(t *testing.T) {
	c, ctx, svc, exec := dumpTestDB(t, store.ServiceKindMongoDB)
	ctx = WithActor(ctx, Actor{Kind: "user", Name: "test", Scope: store.ScopeAdmin})
	exec(`mongosh --quiet "mongodb://$MONGO_INITDB_ROOT_USERNAME:$MONGO_INITDB_ROOT_PASSWORD@127.0.0.1/app?authSource=admin" --eval '
db.items.insertMany([{ n: 1, name: "one", at: new Date(0) }, { n: 2, name: "x".repeat(5000) }, { n: 3, name: "three" }]);
db.createView("v", "items", []);
db.getSiblingDB("other").logs.insertOne({ a: 1 })'`)

	dbs, err := c.PgDatabases(ctx, svc.ID)
	if err != nil || len(dbs) != 2 || dbs[0].Name != "app" || !dbs[0].Main || dbs[1].Name != "other" {
		t.Fatalf("databases = %+v, %v", dbs, err)
	}
	tables, err := c.PgTables(ctx, svc.ID, "")
	if err != nil || len(tables) != 2 || tables[0].Name != "items" || tables[0].Kind != "collection" || tables[0].RowEstimate != 3 ||
		tables[1].Kind != "view" {
		t.Fatalf("collections = %+v, %v", tables, err)
	}
	docs, err := c.MongoDocuments(ctx, svc.ID, "", "items", DocQuery{Limit: 2, Sort: `{"n": 1}`})
	if err != nil || !docs.HasMore || len(docs.Documents) != 2 || !strings.Contains(docs.Documents[0], `"at":{"$date":"1970-01-01T00:00:00Z"}`) ||
		len(docs.Truncated) != 1 || docs.Truncated[0] != 1 {
		t.Fatalf("documents = %+v, %v", docs, err)
	}
	docs, err = c.MongoDocuments(ctx, svc.ID, "", "items", DocQuery{Filter: `{"n": {"$gte": 3}}`})
	if err != nil || len(docs.Documents) != 1 || !strings.Contains(docs.Documents[0], `"three"`) {
		t.Fatalf("filtered = %+v, %v", docs, err)
	}
	for _, f := range []string{`not json`, `{"$where": "sleep(100)"`} {
		if _, err := c.MongoDocuments(ctx, svc.ID, "", "items", DocQuery{Filter: f}); !errors.Is(err, ErrInvalid) {
			t.Errorf("filter %q: %v", f, err)
		}
	}
	if _, err := c.MongoDocuments(ctx, svc.ID, "", "missing", DocQuery{}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing collection: %v", err)
	}
	if docs, err := c.MongoDocuments(ctx, svc.ID, "other", "logs", DocQuery{}); err != nil || len(docs.Documents) != 1 {
		t.Errorf("other database: %+v, %v", docs, err)
	}
	if _, err := c.PgRows(ctx, svc.ID, "", "", "items", RowQuery{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("rows of a collection: %v", err)
	}

	res, err := c.DataConsole(ctx, svc.ID, "", "db.items.countDocuments()", false)
	if err != nil || res.Output != "3" {
		t.Fatalf("console = %+v, %v", res, err)
	}
	if _, err := c.DataConsole(ctx, svc.ID, "", "db.items.deleteMany({})", false); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "allow writes") {
		t.Errorf("read-only delete: %v", err)
	}
	if _, err := c.DataConsole(ctx, svc.ID, "", "db.items.nope()", false); !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), "\n") {
		t.Errorf("script error: %v", err)
	}
	res, err = c.DataConsole(ctx, svc.ID, "other", "db.logs.insertOne({ a: 2 }).acknowledged", true)
	if err != nil || res.Output != "true" {
		t.Fatalf("write = %+v, %v", res, err)
	}
}
