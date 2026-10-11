package core

import (
	"errors"
	"strings"
	"testing"
)

func TestEditStatements(t *testing.T) {
	cols := []PgColumn{{Name: "id", PrimaryKey: true}, {Name: "name"}, {Name: "found"}}
	pg := sqlEdit{table: `"s"."t"`, ident: pgIdent, literal: pgLiteral, textEq: "%s::text = %s", noColumns: "INSERT INTO %s DEFAULT VALUES;"}
	s := func(v string) *string { return &v }
	stmts, err := editStatements(pg, cols, RowChanges{
		Inserts: []map[string]*string{{"name": s("it's"), "found": nil}, {}},
		Updates: []RowUpdate{{Key: map[string]string{"id": "2"}, Set: map[string]*string{"found": s("x"), "name": nil}, Was: map[string]*string{"name": s("b"), "found": nil}}},
		Deletes: []RowDelete{{Key: map[string]string{"id": "3"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`DELETE FROM "s"."t" WHERE "id" = '3';`,
		`UPDATE "s"."t" SET "name" = NULL, "found" = 'x' WHERE "id" = '2' AND "name"::text = 'b' AND "found" IS NULL;`,
		`INSERT INTO "s"."t" ("name", "found") VALUES ('it''s', NULL);`,
		`INSERT INTO "s"."t" DEFAULT VALUES;`,
	}
	for i, st := range stmts {
		if st.sql != want[i] {
			t.Errorf("statement %d:\n got %s\nwant %s", i, st.sql, want[i])
		}
	}
	if stmts[1].label != "row id 2" || stmts[1].where != `"id" = '2' AND "name"::text = 'b' AND "found" IS NULL` || stmts[2].where != "" {
		t.Errorf("update: %+v", stmts[1])
	}

	for name, ch := range map[string]RowChanges{
		"empty":          {},
		"unknown column": {Inserts: []map[string]*string{{"nope": nil}}},
		"partial key":    {Deletes: []RowDelete{{Key: map[string]string{"name": "a"}}}},
		"no set":         {Updates: []RowUpdate{{Key: map[string]string{"id": "1"}}}},
		"no was":         {Updates: []RowUpdate{{Key: map[string]string{"id": "1"}, Set: map[string]*string{"name": nil}}}},
		"NUL":            {Inserts: []map[string]*string{{"name": s("a\x00b")}}},
		"locked":         {Updates: []RowUpdate{{Key: map[string]string{"id": "1"}, Set: map[string]*string{"found": nil}, Was: map[string]*string{"found": nil}}}},
	} {
		d := pg
		d.locked = map[string]string{"found": "bytes"}
		if _, err := editStatements(d, cols, ch); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	noKey := []PgColumn{{Name: "a"}}
	if _, err := editStatements(pg, noKey, RowChanges{Deletes: []RowDelete{{Key: map[string]string{}}}}); err == nil || !strings.Contains(err.Error(), "no primary key") {
		t.Errorf("no primary key: %v", err)
	}
	if _, err := editStatements(pg, noKey, RowChanges{Inserts: []map[string]*string{{"a": nil}}}); err != nil {
		t.Errorf("insert without primary key: %v", err)
	}
}
