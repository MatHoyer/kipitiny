package data

import (
	"errors"
	"testing"
)

func TestSQLiteSelect(t *testing.T) {
	cols := []PgColumn{{Name: "id", PrimaryKey: true}, {Name: `we"ird`}}
	got, err := sqliteSelect(`t"x`, cols, RowQuery{Limit: 10, Offset: 20, Search: `50%_\`,
		Filters: []PgFilter{{Column: `we"ird`, Op: "=", Value: "it's"}, {Column: "id", Op: "null"}}}, false)
	want := `SELECT "id", "we""ird" FROM "t""x" WHERE "we""ird" = 'it''s' AND "id" IS NULL AND ` +
		`(CAST("id" AS TEXT) LIKE '%50\%\_\\%' ESCAPE '\' OR CAST("we""ird" AS TEXT) LIKE '%50\%\_\\%' ESCAPE '\') ORDER BY "id"`
	if err != nil || got != want {
		t.Errorf("select:\n got %s %v\nwant %s", got, err, want)
	}
	for _, q := range []RowQuery{{OrderBy: "nope"}, {Filters: []PgFilter{{Column: "nope", Op: "="}}}, {Filters: []PgFilter{{Column: "id", Op: "~"}}}, {Search: "a\x00"}} {
		if _, err := sqliteSelect("t", cols, q, true); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", q, err)
		}
	}
}
