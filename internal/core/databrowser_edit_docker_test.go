package core

import (
	"errors"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestRowChangesDockerMySQL(t *testing.T) {
	for _, kind := range []domain.ServiceKind{domain.ServiceKindMySQL, domain.ServiceKindMariaDB} {
		t.Run(string(kind), func(t *testing.T) {
			c, ctx, svc, exec := dumpTestDB(t, kind)
			ctx = identity.WithActor(ctx, identity.Actor{Kind: "user", Name: "test", Role: domain.RoleAdmin})
			client := `MYSQL_PWD="$MYSQL_PASSWORD" ` + data.MySQLBin(kind, "mysql") + ` -h 127.0.0.1 -u"$MYSQL_USER" "$MYSQL_DATABASE" `
			exec(client + `-e "
CREATE TABLE items (id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(50) NOT NULL DEFAULT 'none', b BLOB, n INT NULL);
INSERT INTO items (name, n) VALUES ('Alice', 1), ('Bob', NULL), ('c', 3);
CREATE VIEW v AS SELECT id FROM items;"`)
			s := func(v string) *string { return &v }
			ch := data.RowChanges{
				Deletes: []data.RowDelete{{Key: map[string]string{"id": "3"}}},
				Updates: []data.RowUpdate{{Key: map[string]string{"id": "1"}, Set: map[string]*string{"name": s(`it's \n`), "n": nil},
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
			stale := data.RowChanges{
				Deletes: []data.RowDelete{{Key: map[string]string{"id": "4"}}},
				Updates: []data.RowUpdate{{Key: map[string]string{"id": "2"}, Set: map[string]*string{"name": s("x")}, Was: map[string]*string{"name": s("bob")}}},
			}
			if _, err = c.ApplyRowChanges(ctx, svc.ID, "", "", "items", stale, true); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "row id 2 changed or was deleted") {
				t.Errorf("stale update: %v", err)
			}
			if got := exec(client + `-N -e "SELECT COUNT(*) FROM items WHERE id = 4"`); got != "1" {
				t.Errorf("stale save kept its delete")
			}
			_, err = c.ApplyRowChanges(ctx, svc.ID, "", "", "items", data.RowChanges{Updates: []data.RowUpdate{{Key: map[string]string{"id": "2"}, Set: map[string]*string{"b": nil}, Was: map[string]*string{"b": nil}}}}, false)
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "bytes") {
				t.Errorf("binary column: %v", err)
			}
			if _, err = c.ApplyRowChanges(ctx, svc.ID, "", "", "v", data.RowChanges{Inserts: []map[string]*string{{}}}, true); !errors.Is(err, ErrInvalid) {
				t.Errorf("view: %v", err)
			}
		})
	}
}

func TestDocChangesDockerMongo(t *testing.T) {
	c, ctx, svc, exec := dumpTestDB(t, domain.ServiceKindMongoDB)
	ctx = identity.WithActor(ctx, identity.Actor{Kind: "user", Name: "test", Role: domain.RoleAdmin})
	shell := `mongosh --quiet "mongodb://$MONGO_INITDB_ROOT_USERNAME:$MONGO_INITDB_ROOT_PASSWORD@127.0.0.1/app?authSource=admin" --eval `
	exec(shell + `'
db.items.insertMany([{ _id: 1, n: NumberLong(5), name: "one", tags: ["a"] }, { _id: 2, name: "two" }, { _id: 3, name: "three" }]);
db.createView("v", "items", [])'`)
	s := func(v string) *string { return &v }
	ch := data.DocChanges{
		Deletes: []data.DocDelete{{ID: "3"}},
		Updates: []data.DocUpdate{{ID: "1", Set: map[string]string{"name": `"it's < 1"`, "extra": `{"$date": "2020-01-01T00:00:00Z"}`}, Unset: []string{"tags"},
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

	stale := data.DocChanges{
		Deletes: []data.DocDelete{{ID: "2"}},
		Updates: []data.DocUpdate{{ID: "1", Set: map[string]string{"name": `"x"`}, Was: map[string]*string{"name": s(`"one"`)}}},
	}
	_, err = c.ApplyDocChanges(ctx, svc.ID, "", "items", stale, true)
	if !errors.Is(err, ErrInvalid) || !strings.HasPrefix(err.Error(), "invalid input: document 1 changed or was deleted") {
		t.Errorf("stale update: %v", err)
	}
	if got := exec(shell + `'print(db.items.countDocuments({ _id: 2 }))'`); got != "1" {
		t.Errorf("stale save ran its delete")
	}
	// A duplicate _id fails the second insert: the first stays.
	_, err = c.ApplyDocChanges(ctx, svc.ID, "", "items", data.DocChanges{Inserts: []string{`{"_id": 5}`, `{"_id": 2}`}}, true)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "duplicate key") || !strings.HasSuffix(err.Error(), "(the change before it was saved)") {
		t.Errorf("duplicate: %v", err)
	}
	for name, ch := range map[string]data.DocChanges{
		"bad json":  {Inserts: []string{`{nope`}},
		"not doc":   {Inserts: []string{`[1]`}},
		"_id":       {Updates: []data.DocUpdate{{ID: "1", Set: map[string]string{"_id": "9"}, Was: map[string]*string{"_id": s("1")}}}},
		"dotted":    {Updates: []data.DocUpdate{{ID: "1", Set: map[string]string{"a.b": "9"}, Was: map[string]*string{"a.b": nil}}}},
		"no was":    {Updates: []data.DocUpdate{{ID: "1", Set: map[string]string{"a": "9"}}}},
		"set+unset": {Updates: []data.DocUpdate{{ID: "1", Set: map[string]string{"a": "9"}, Unset: []string{"a"}, Was: map[string]*string{"a": nil}}}},
	} {
		if _, err := c.ApplyDocChanges(ctx, svc.ID, "", "items", ch, false); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err = c.ApplyDocChanges(ctx, svc.ID, "", "v", data.DocChanges{Inserts: []string{`{}`}}, true); !errors.Is(err, ErrInvalid) {
		t.Errorf("view: %v", err)
	}
}
