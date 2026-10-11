package core

import (
	"errors"
	"testing"
)

func TestCreateTableSQL(t *testing.T) {
	sql, err := createTableSQL(`"public"."t"`, pgIdent, []NewColumn{
		{Name: "id", Type: "bigserial", PrimaryKey: true},
		{Name: "name", Type: "varchar(50)", Default: "'none'"},
		{Name: "tags", Type: "text[]", Nullable: true},
	})
	want := `CREATE TABLE "public"."t" (
  "id" bigserial,
  "name" varchar(50) NOT NULL DEFAULT 'none',
  "tags" text[],
  PRIMARY KEY ("id")
);`
	if err != nil || sql != want {
		t.Fatalf("got %v\n%s", err, sql)
	}
	for name, cols := range map[string][]NewColumn{
		"none":      nil,
		"no name":   {{Type: "int"}},
		"twice":     {{Name: "a", Type: "int"}, {Name: "a", Type: "int"}},
		"bad type":  {{Name: "a", Type: "int); DROP TABLE x; --"}},
		"statement": {{Name: "a", Type: "int", Default: "1; DROP TABLE x"}},
		"comment":   {{Name: "a", Type: "int", Default: "1 -- x"}},
	} {
		if _, err := createTableSQL("t", pgIdent, cols); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
