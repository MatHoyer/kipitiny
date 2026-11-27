package sqlitehelper

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, req Request) Response {
	t.Helper()
	in, _ := json.Marshal(req)
	var out bytes.Buffer
	if code := Main(nil, bytes.NewReader(in), &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	var res Response
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	return res
}

func fixture(t *testing.T) (root, file string) {
	t.Helper()
	root = t.TempDir()
	file = filepath.Join(root, "data", "app.db")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+file+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, avatar BLOB);
INSERT INTO users (name, avatar) VALUES ('ada', x'0102'), ('bob', NULL), ('` + strings.Repeat("é", 10) + `', NULL);`)
	if err != nil {
		t.Fatal(err)
	}
	return root, file
}

func TestQueryReadOnly(t *testing.T) {
	root, file := fixture(t)
	res := run(t, Request{Op: OpQuery, Root: root, File: file, SQL: "SELECT id, name, avatar FROM users ORDER BY id", Rows: 2, Cell: 100})
	if res.Error != "" {
		t.Fatal(res.Error)
	}
	if len(res.Rows) != 2 || !res.More || strings.Join(res.Columns, ",") != "id,name,avatar" {
		t.Fatalf("rows: %+v", res)
	}
	if *res.Rows[0][2] != "0x0102" || res.Rows[1][2] != nil {
		t.Errorf("cells: %v %v", *res.Rows[0][2], res.Rows[1][2])
	}
	// Writes are refused by SQLite itself, whatever the SQL tries.
	for _, q := range []string{"DELETE FROM users", "PRAGMA query_only = 0; DELETE FROM users"} {
		if res := run(t, Request{Op: OpQuery, Root: root, File: file, SQL: q}); !strings.Contains(res.Error, "readonly") {
			t.Errorf("%s: %+v", q, res)
		}
	}
	// No other file is opened, read-only or not: ATTACH (and VACUUM INTO,
	// which attaches) would create or change one.
	for _, write := range []bool{false, true} {
		for _, q := range []string{"ATTACH 'file:" + root + "/x.db?mode=rwc' AS x", "ATTACH 'file:" + file + "?mode=rw' AS b", "VACUUM INTO '" + root + "/x.db'"} {
			if res := run(t, Request{Op: OpQuery, Root: root, File: file, SQL: q, Write: write}); res.Error == "" || strings.Contains(res.Error, "too many") {
				t.Errorf("%s (write %v): %+v", q, write, res)
			}
		}
	}
	if _, err := os.Stat(root + "/x.db"); err == nil {
		t.Error("a file was created")
	}
	// A cut cell keeps whole characters.
	res = run(t, Request{Op: OpQuery, Root: root, File: file, SQL: "SELECT name FROM users WHERE id = 3", Cell: 5})
	if got := *res.Rows[0][0]; got != "éé" || len(res.Truncated) != 1 {
		t.Errorf("cut: %q %v", got, res.Truncated)
	}
}

func TestQueryWrite(t *testing.T) {
	root, file := fixture(t)
	res := run(t, Request{Op: OpQuery, Root: root, File: file, Write: true, SQL: "UPDATE users SET name = 'x' WHERE id < 3"})
	if res.Error != "" || res.Changes != 2 || res.Columns != nil {
		t.Fatalf("%+v", res)
	}
	// A failing statement rolls the whole run back.
	res = run(t, Request{Op: OpQuery, Root: root, File: file, Write: true, SQL: "DELETE FROM users; SELECT * FROM nope"})
	if res.Error == "" {
		t.Fatal("no error")
	}
	res = run(t, Request{Op: OpQuery, Root: root, File: file, SQL: "SELECT count(*) FROM users"})
	if *res.Rows[0][0] != "3" {
		t.Errorf("rolled back: %v", *res.Rows[0][0])
	}
}

func TestApply(t *testing.T) {
	root, file := fixture(t)
	res := run(t, Request{Op: OpApply, Root: root, File: file, Write: true, Statements: []Statement{
		{SQL: "UPDATE users SET name = 'z' WHERE id = 1", One: true},
		{SQL: "DELETE FROM users WHERE id = 99", One: true},
	}})
	if res.Failed != 2 {
		t.Fatalf("%+v", res)
	}
	res = run(t, Request{Op: OpQuery, Root: root, File: file, SQL: "SELECT name FROM users WHERE id = 1"})
	if *res.Rows[0][0] != "ada" {
		t.Errorf("not rolled back: %v", *res.Rows[0][0])
	}
}

func TestResolveAndFind(t *testing.T) {
	root, file := fixture(t)
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte(strings.Repeat("x", 200)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file, filepath.Join(other, "link.db")); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(other, filepath.Join(other, "link.db")); err == nil {
		t.Error("a symlink out of its volume was accepted")
	}
	if _, err := Resolve(root, filepath.Join(root, "notes.txt")); err == nil {
		t.Error("a text file was accepted")
	}
	res := run(t, Request{Op: OpFind, Roots: []string{root, other}})
	if len(res.Files) != 1 || res.Files[0].Path != file {
		t.Errorf("found: %+v", res.Files)
	}
}

func TestExport(t *testing.T) {
	root, file := fixture(t)
	in, _ := json.Marshal(Request{Op: OpCSV, Root: root, File: file, SQL: "SELECT id, avatar FROM users ORDER BY id"})
	var out bytes.Buffer
	if code := Main(nil, bytes.NewReader(in), &out); code != 0 {
		t.Fatal(code)
	}
	if got := out.String(); got != "id,avatar\n1,0x0102\n2,\n3,\n" {
		t.Errorf("csv: %q", got)
	}
}
