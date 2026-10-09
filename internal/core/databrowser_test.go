package core

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/docker"
)

func TestPgSelect(t *testing.T) {
	cols := []PgColumn{{Name: "id", PrimaryKey: true}, {Name: `we"ird`}, {Name: "tenant", PrimaryKey: true}}
	for name, tc := range map[string]struct {
		q    RowQuery
		page bool
		want string
	}{
		"page by primary key": {
			q: RowQuery{Limit: 50, Offset: 100}, page: true,
			want: `SELECT json_build_array(left("id"::text, 4097), left("we""ird"::text, 4097), left("tenant"::text, 4097)) FROM "public"."t" ORDER BY "id", "tenant" LIMIT 51 OFFSET 100;`,
		},
		"filters and sort": {
			q: RowQuery{Limit: 10, OrderBy: `we"ird`, Desc: true, Filters: []PgFilter{
				{Column: "id", Op: ">=", Value: "3"},
				{Column: `we"ird`, Op: "like", Value: "%o'k%"},
				{Column: "tenant", Op: "null", Value: "ignored"},
			}}, page: true,
			want: `SELECT json_build_array(left("id"::text, 4097), left("we""ird"::text, 4097), left("tenant"::text, 4097)) FROM "public"."t" WHERE "id" >= '3' AND "we""ird"::text ILIKE '%o''k%' AND "tenant" IS NULL ORDER BY "we""ird" DESC LIMIT 11 OFFSET 0;`,
		},
		"search": {
			q: RowQuery{Limit: 10, Search: `50%_o'k\`}, page: true,
			want: `SELECT json_build_array(left("id"::text, 4097), left("we""ird"::text, 4097), left("tenant"::text, 4097)) FROM "public"."t" WHERE ("id"::text ILIKE '%50\%\_o''k\\%' OR "we""ird"::text ILIKE '%50\%\_o''k\\%' OR "tenant"::text ILIKE '%50\%\_o''k\\%') ORDER BY "id", "tenant" LIMIT 11 OFFSET 0;`,
		},
		"export": {
			q:    RowQuery{Limit: 10, Offset: 5, Desc: true},
			want: `SELECT "id", "we""ird", "tenant" FROM "public"."t" ORDER BY "id" DESC, "tenant" DESC;`,
		},
	} {
		got, err := pgSelect("public", "t", cols, tc.q, tc.page)
		if err != nil || got != tc.want {
			t.Errorf("%s:\n got %s (%v)\nwant %s", name, got, err, tc.want)
		}
	}
	for name, q := range map[string]RowQuery{
		"unknown order":     {OrderBy: "nope"},
		"unknown column":    {Filters: []PgFilter{{Column: "id; drop table t", Op: "="}}},
		"unknown operator":  {Filters: []PgFilter{{Column: "id", Op: "; drop"}}},
		"injected operator": {Filters: []PgFilter{{Column: "id", Op: "= 1 OR 1"}}},
	} {
		if _, err := pgSelect("public", "t", cols, q, true); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
}

func TestClientError(t *testing.T) {
	for stderr, want := range map[string]string{
		"psql:<stdin>:1: ERROR:  relation \"x\" does not exist\nLINE 1: SELECT": "relation \"x\" does not exist\nLINE 1: SELECT",
		"(error) WRONGTYPE Operation against a key":                             "WRONGTYPE Operation against a key",
		"connection refused": "connection refused",
	} {
		err := clientError(&docker.ExecError{ExitCode: 1, Stderr: stderr})
		if !errors.Is(err, ErrInvalid) || !strings.HasSuffix(err.Error(), ": "+want) {
			t.Errorf("%q: got %v", stderr, err)
		}
	}
	other := errors.New("exec attach: boom")
	if err := clientError(other); err != other {
		t.Errorf("non-exec error changed: %v", err)
	}
}

func TestTruncate(t *testing.T) {
	if s, cut := truncate("short"); s != "short" || cut {
		t.Errorf("short: %q %v", s, cut)
	}
	long := strings.Repeat("a", DataCellMax-1) + "é" // é straddles the cap
	s, cut := truncate(long + "rest")
	if !cut || len(s) != DataCellMax-1 {
		t.Errorf("long: len %d cut %v", len(s), cut)
	}
}

func TestSplitRedisArgs(t *testing.T) {
	for line, want := range map[string][]string{
		`GET key`:                     {"GET", "key"},
		`  SET  "a b" 'it\'s' `:       {"SET", "a b", "it's"},
		`SET k "line\nnext\x41\"q\\"`: {"SET", "k", "line\nnextA\"q\\"},
		`SET k ""`:                    {"SET", "k", ""},
		`HSET h f 'raw \n'`:           {"HSET", "h", "f", `raw \n`},
		"":                            nil,
	} {
		got, err := splitRedisArgs(line)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("%q: got %q (%v), want %q", line, got, err, want)
		}
	}
	for _, line := range []string{`GET "open`, `GET 'open`, `GET "a"b`, `GET "\x4"`, `GET "\xzz"`} {
		if _, err := splitRedisArgs(line); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: got %v, want ErrInvalid", line, err)
		}
	}
}

func TestPgTagRe(t *testing.T) {
	for tag, want := range map[string]bool{
		"UPDATE 3": true, "INSERT 0 1": true, "CREATE TABLE": true, "BEGIN": true,
		"count": false, "id,name": false, "UPDATE 3 extra words": false,
	} {
		if got := pgTagRe.MatchString(tag); got != want {
			t.Errorf("%q: got %v", tag, got)
		}
	}
}

func TestCapWriter(t *testing.T) {
	w := &capWriter{max: 5}
	for _, s := range []string{"abc", "def", "ghi"} {
		if n, err := w.Write([]byte(s)); n != 3 || err != nil {
			t.Fatalf("write %q: %d %v", s, n, err)
		}
	}
	if w.b.String() != "abcde" || !w.dropped {
		t.Errorf("got %q dropped %v", w.b.String(), w.dropped)
	}
}
