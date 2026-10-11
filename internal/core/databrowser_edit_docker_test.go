package core

import (
	"errors"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"

	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestRowChangesDockerPostgres(t *testing.T) {
	c, ctx, svc, exec := dataTestDB(t, store.ServiceKindPostgres, &container.Config{
		Image: DefaultPostgresImage,
		Env:   []string{"POSTGRES_USER=app", "POSTGRES_DB=app", "POSTGRES_PASSWORD=secret"},
	})
	exec(`psql -U app -d app -v ON_ERROR_STOP=1 -c "
CREATE TABLE items (id serial PRIMARY KEY, name text NOT NULL DEFAULT 'none', found boolean, doc jsonb);
INSERT INTO items (name, found, doc) VALUES ('a', true, '{\"k\": 1}'), ('b', NULL, NULL), ('c', false, NULL);
CREATE VIEW v AS SELECT id FROM items;"`)
	s := func(v string) *string { return &v }
	query := func(sql string) string { return exec(`psql -U app -d app -At -c "` + sql + `"`) }

	ch := RowChanges{
		Deletes: []RowDelete{{Key: map[string]string{"id": "3"}}},
		Updates: []RowUpdate{{Key: map[string]string{"id": "1"}, Set: map[string]*string{"name": s("it's\nnew"), "found": nil, "doc": s(`{"k": 2}`)},
			Was: map[string]*string{"name": s("a"), "found": s("true"), "doc": s(`{"k": 1}`)}}},
		Inserts: []map[string]*string{{"found": s("true")}},
	}
	res, err := c.ApplyRowChanges(ctx, svc.ID, "", "public", "items", ch, false)
	if err != nil || res.Applied || len(res.Statements) != 3 {
		t.Fatalf("preview = %+v, %v", res, err)
	}
	if got := query("SELECT count(*) FROM items"); got != "3" {
		t.Fatalf("preview changed rows: %s", got)
	}
	if res, err = c.ApplyRowChanges(ctx, svc.ID, "", "public", "items", ch, true); err != nil || !res.Applied {
		t.Fatalf("apply = %+v, %v", res, err)
	}
	if got := query("SELECT id, name, found IS NULL, doc FROM items ORDER BY id"); got != "1|it's\nnew|t|{\"k\": 2}\n2|b|t|\n4|none|f|" {
		t.Errorf("rows after save:\n%s", got)
	}

	// The row changed since it was loaded: nothing of the save stays.
	stale := RowChanges{
		Deletes: []RowDelete{{Key: map[string]string{"id": "2"}}},
		Updates: []RowUpdate{{Key: map[string]string{"id": "1"}, Set: map[string]*string{"name": s("x")}, Was: map[string]*string{"name": s("a")}}},
	}
	_, err = c.ApplyRowChanges(ctx, svc.ID, "", "public", "items", stale, true)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "row id 1 changed or was deleted") || strings.Contains(err.Error(), "CONTEXT") {
		t.Errorf("stale update: %v", err)
	}
	if got := query("SELECT count(*) FROM items WHERE id = 2"); got != "1" {
		t.Errorf("stale save kept its delete")
	}
	_, err = c.ApplyRowChanges(ctx, svc.ID, "", "public", "items", RowChanges{Inserts: []map[string]*string{{"name": nil}}}, true)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "not-null") {
		t.Errorf("NULL in NOT NULL: %v", err)
	}
	_, err = c.ApplyRowChanges(ctx, svc.ID, "", "public", "v", RowChanges{Inserts: []map[string]*string{{}}}, true)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "view") {
		t.Errorf("view: %v", err)
	}
	if _, err = c.ApplyRowChanges(ctx, svc.ID, "", "public", "missing", RowChanges{Inserts: []map[string]*string{{}}}, true); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing table: %v", err)
	}
	read := WithActor(ctx, Actor{Kind: "token", Name: "read", Scope: store.ScopeRead})
	if _, err = c.ApplyRowChanges(read, svc.ID, "", "public", "items", ch, false); err == nil {
		t.Error("read scope previewed changes")
	}
}

func TestRowChangesDockerMySQL(t *testing.T) {
	for _, kind := range []store.ServiceKind{store.ServiceKindMySQL, store.ServiceKindMariaDB} {
		t.Run(string(kind), func(t *testing.T) {
			c, ctx, svc, exec := dumpTestDB(t, kind)
			ctx = WithActor(ctx, Actor{Kind: "user", Name: "test", Scope: store.ScopeAdmin})
			client := `MYSQL_PWD="$MYSQL_PASSWORD" ` + mysqlBin(kind, "mysql") + ` -h 127.0.0.1 -u"$MYSQL_USER" "$MYSQL_DATABASE" `
			exec(client + `-e "
CREATE TABLE items (id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(50) NOT NULL DEFAULT 'none', b BLOB, n INT NULL);
INSERT INTO items (name, n) VALUES ('Alice', 1), ('Bob', NULL), ('c', 3);
CREATE VIEW v AS SELECT id FROM items;"`)
			s := func(v string) *string { return &v }
			ch := RowChanges{
				Deletes: []RowDelete{{Key: map[string]string{"id": "3"}}},
				Updates: []RowUpdate{{Key: map[string]string{"id": "1"}, Set: map[string]*string{"name": s(`it's \n`), "n": nil},
					Was: map[string]*string{"name": s("Alice"), "n": s("1")}}},
				Inserts: []map[string]*string{{"n": s("9")}},
			}
			res, err := c.ApplyRowChanges(ctx, svc.ID, "", "app", "items", ch, true)
			if err != nil || !res.Applied || res.Statements[1] != "UPDATE `items` SET `name` = 'it''s \\n', `n` = NULL WHERE `id` = '1' AND CAST(`name` AS CHAR) COLLATE utf8mb4_bin = 'Alice' AND CAST(`n` AS CHAR) COLLATE utf8mb4_bin = '1';" {
				t.Fatalf("apply = %+v, %v", res, err)
			}
			if got := exec(client + `-N -B --raw -e "SELECT id, name, COALESCE(n, 'NULL') FROM items ORDER BY id"`); got != "1\tit's \\n\tNULL\n2\tBob\tNULL\n4\tnone\t9" {
				t.Errorf("rows after save:\n%s", got)
			}
			// Case counts: "bob" isn't what was loaded.
			stale := RowChanges{
				Deletes: []RowDelete{{Key: map[string]string{"id": "4"}}},
				Updates: []RowUpdate{{Key: map[string]string{"id": "2"}, Set: map[string]*string{"name": s("x")}, Was: map[string]*string{"name": s("bob")}}},
			}
			if _, err = c.ApplyRowChanges(ctx, svc.ID, "", "", "items", stale, true); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "row id 2 changed or was deleted") {
				t.Errorf("stale update: %v", err)
			}
			if got := exec(client + `-N -e "SELECT COUNT(*) FROM items WHERE id = 4"`); got != "1" {
				t.Errorf("stale save kept its delete")
			}
			_, err = c.ApplyRowChanges(ctx, svc.ID, "", "", "items", RowChanges{Updates: []RowUpdate{{Key: map[string]string{"id": "2"}, Set: map[string]*string{"b": nil}, Was: map[string]*string{"b": nil}}}}, false)
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "bytes") {
				t.Errorf("binary column: %v", err)
			}
			if _, err = c.ApplyRowChanges(ctx, svc.ID, "", "", "v", RowChanges{Inserts: []map[string]*string{{}}}, true); !errors.Is(err, ErrInvalid) {
				t.Errorf("view: %v", err)
			}
		})
	}
}

func TestDocChangesDockerMongo(t *testing.T) {
	c, ctx, svc, exec := dumpTestDB(t, store.ServiceKindMongoDB)
	ctx = WithActor(ctx, Actor{Kind: "user", Name: "test", Scope: store.ScopeAdmin})
	shell := `mongosh --quiet "mongodb://$MONGO_INITDB_ROOT_USERNAME:$MONGO_INITDB_ROOT_PASSWORD@127.0.0.1/app?authSource=admin" --eval `
	exec(shell + `'
db.items.insertMany([{ _id: 1, n: NumberLong(5), name: "one", tags: ["a"] }, { _id: 2, name: "two" }, { _id: 3, name: "three" }]);
db.createView("v", "items", [])'`)
	s := func(v string) *string { return &v }
	ch := DocChanges{
		Deletes: []DocDelete{{ID: "3"}},
		Updates: []DocUpdate{{ID: "1", Set: map[string]string{"name": `"it's < 1"`, "extra": `{"$date": "2020-01-01T00:00:00Z"}`}, Unset: []string{"tags"},
			Was: map[string]*string{"name": s(`"one"`), "extra": nil, "tags": s(`["a"]`)}}},
		Inserts: []string{`{"_id": 4, "name": "four"}`},
	}
	res, err := c.ApplyDocChanges(ctx, svc.ID, "", "items", ch, false)
	if err != nil || res.Applied || len(res.Statements) != 3 ||
		res.Statements[1] != `db.items.updateOne({"_id": 1, "extra": {"$exists": false}, "name": "one", "tags": ["a"]}, {"$set": {"extra": {"$date":"2020-01-01T00:00:00Z"}, "name": "it's < 1"}, "$unset": {"tags": ""}})` {
		t.Fatalf("preview = %#v, %v", res, err)
	}
	if res, err = c.ApplyDocChanges(ctx, svc.ID, "", "items", ch, true); err != nil || !res.Applied {
		t.Fatalf("apply = %+v, %v", res, err)
	}
	// n keeps its type: the update only touched the fields it changed.
	got := exec(shell + `'print(EJSON.stringify(db.items.find().sort({ _id: 1 }).toArray(), { relaxed: false }))'`)
	if got != `[{"_id":{"$numberInt":"1"},"n":{"$numberLong":"5"},"name":"it's < 1","extra":{"$date":{"$numberLong":"1577836800000"}}},{"_id":{"$numberInt":"2"},"name":"two"},{"_id":{"$numberInt":"4"},"name":"four"}]` {
		t.Errorf("documents after save:\n%s", got)
	}

	stale := DocChanges{
		Deletes: []DocDelete{{ID: "2"}},
		Updates: []DocUpdate{{ID: "1", Set: map[string]string{"name": `"x"`}, Was: map[string]*string{"name": s(`"one"`)}}},
	}
	_, err = c.ApplyDocChanges(ctx, svc.ID, "", "items", stale, true)
	if !errors.Is(err, ErrInvalid) || !strings.HasPrefix(err.Error(), "invalid input: document 1 changed or was deleted") {
		t.Errorf("stale update: %v", err)
	}
	if got := exec(shell + `'print(db.items.countDocuments({ _id: 2 }))'`); got != "1" {
		t.Errorf("stale save ran its delete")
	}
	// A duplicate _id fails the second insert: the first stays.
	_, err = c.ApplyDocChanges(ctx, svc.ID, "", "items", DocChanges{Inserts: []string{`{"_id": 5}`, `{"_id": 2}`}}, true)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "duplicate key") || !strings.HasSuffix(err.Error(), "(the change before it was saved)") {
		t.Errorf("duplicate: %v", err)
	}
	for name, ch := range map[string]DocChanges{
		"bad json":  {Inserts: []string{`{nope`}},
		"not doc":   {Inserts: []string{`[1]`}},
		"_id":       {Updates: []DocUpdate{{ID: "1", Set: map[string]string{"_id": "9"}, Was: map[string]*string{"_id": s("1")}}}},
		"dotted":    {Updates: []DocUpdate{{ID: "1", Set: map[string]string{"a.b": "9"}, Was: map[string]*string{"a.b": nil}}}},
		"no was":    {Updates: []DocUpdate{{ID: "1", Set: map[string]string{"a": "9"}}}},
		"set+unset": {Updates: []DocUpdate{{ID: "1", Set: map[string]string{"a": "9"}, Unset: []string{"a"}, Was: map[string]*string{"a": nil}}}},
	} {
		if _, err := c.ApplyDocChanges(ctx, svc.ID, "", "items", ch, false); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err = c.ApplyDocChanges(ctx, svc.ID, "", "v", DocChanges{Inserts: []string{`{}`}}, true); !errors.Is(err, ErrInvalid) {
		t.Errorf("view: %v", err)
	}
}
