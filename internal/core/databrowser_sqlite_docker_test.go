package core

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/store"
)

// sqliteSeed is a script writing a small SQLite database, owned by 1000, to
// /seed/db/app.db.
func sqliteSeed(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE teams (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE users (id INTEGER PRIMARY KEY, team_id INTEGER REFERENCES teams, name TEXT, avatar BLOB);
CREATE VIEW names AS SELECT name FROM users;
INSERT INTO teams (name) VALUES ('core');
INSERT INTO users (team_id, name, avatar) VALUES (1, 'ada', x'00ff'), (1, 'bob', NULL);`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return "mkdir -p /seed/db && echo " + base64.StdEncoding.EncodeToString(raw) + " | base64 -d > /seed/db/app.db && chown -R 1000:1000 /seed/db"
}

// The SQLite browser runs the manager's own image: name one built from this
// tree (scripts/test-env.sh up builds ghcr.io/kipit-labs/kipitiny:dev-<branch>).
//
//	KIPITINY_TEST_DOCKER=1 KIPITINY_TEST_SQLITE_IMAGE=ghcr.io/kipit-labs/kipitiny:dev-x go test ./internal/core/ -run SQLiteDocker
func TestDataBrowserSQLiteDocker(t *testing.T) {
	image := os.Getenv("KIPITINY_TEST_SQLITE_IMAGE")
	if image == "" {
		t.Skip("KIPITINY_TEST_SQLITE_IMAGE not set")
	}
	c, _, svc := filesTestService(t, store.Service{Name: "web", Kind: store.ServiceKindApp,
		Volumes: []store.Volume{{Name: "data", Path: "/data"}}}, "data", sqliteSeed(t))
	name, tag, _ := strings.Cut(image, ":")
	c.cfg.Update.Image, c.cfg.Version = name, tag
	ctx := WithActor(context.Background(), Actor{Kind: "user", Name: "test", Role: store.RoleAdmin})

	dbs, err := c.PgDatabases(ctx, svc.ID)
	if err != nil || len(dbs) != 1 || dbs[0].Name != "data/db/app.db" || dbs[0].Bytes == 0 {
		t.Fatalf("databases: %+v %v", dbs, err)
	}
	db := dbs[0].Name

	tables, err := c.PgTables(ctx, svc.ID, "")
	if err != nil || len(tables) != 3 {
		t.Fatalf("tables: %+v %v", tables, err)
	}
	users := tables[2]
	if users.Name != "users" || users.Kind != "table" || len(users.Columns) != 4 || !users.Columns[0].PrimaryKey {
		t.Fatalf("users: %+v", users)
	}
	if ref := users.Columns[1].References; ref == nil || ref.Table != "teams" || ref.Column != "id" {
		t.Errorf("foreign key: %+v", ref)
	}
	if tables[0].Name != "names" || tables[0].Kind != "view" {
		t.Errorf("view: %+v", tables[0])
	}

	rows, err := c.PgRows(ctx, svc.ID, db, "main", "users", RowQuery{Limit: 1, Search: "D"})
	if err != nil || len(rows.Rows) != 1 || rows.HasMore {
		t.Fatalf("search: %+v %v", rows, err)
	}
	if got := *rows.Rows[0][3]; got != "0x00ff" {
		t.Errorf("blob: %q", got)
	}
	if rows, err := c.PgRows(ctx, svc.ID, db, "main", "users", RowQuery{Limit: 1, OrderBy: "name", Desc: true}); err != nil ||
		len(rows.Rows) != 1 || !rows.HasMore || *rows.Rows[0][2] != "bob" {
		t.Errorf("page: %+v %v", rows, err)
	}
	if _, err := c.PgRows(ctx, svc.ID, db, "main", "users", RowQuery{OrderBy: "nope"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown column: %v", err)
	}

	var csv bytes.Buffer
	if err := c.PgExport(ctx, svc.ID, db, "main", "users", RowQuery{Filters: []PgFilter{{Column: "name", Op: "=", Value: "bob"}}}, &csv); err != nil ||
		csv.String() != "id,team_id,name,avatar\n2,1,bob,\n" {
		t.Errorf("export: %q %v", csv.String(), err)
	}

	if _, err := c.DataConsole(ctx, svc.ID, db, "DELETE FROM users", false); err == nil || !strings.Contains(err.Error(), "read only") {
		t.Errorf("read-only console: %v", err)
	}
	res, err := c.DataConsole(ctx, svc.ID, db, "UPDATE users SET name = upper(name)", true)
	if err != nil || res.Output != "OK, 2 rows changed" {
		t.Errorf("write console: %+v %v", res, err)
	}

	bob := "BOB"
	ch := RowChanges{Updates: []RowUpdate{{Key: map[string]string{"id": "2"}, Set: map[string]*string{"name": new("bobby")}, Was: map[string]*string{"name": &bob}}}}
	if _, err := c.ApplyRowChanges(ctx, svc.ID, db, "main", "users", ch, true); err != nil {
		t.Errorf("edit: %v", err)
	}
	// Applied again, its row no longer reads as loaded.
	if _, err := c.ApplyRowChanges(ctx, svc.ID, db, "main", "users", ch, true); err == nil || !strings.Contains(err.Error(), "changed or was deleted") {
		t.Errorf("stale edit: %v", err)
	}
	if _, err := c.ApplyRowChanges(ctx, svc.ID, db, "main", "users",
		RowChanges{Inserts: []map[string]*string{{"avatar": new("0x00")}}}, true); err == nil || !strings.Contains(err.Error(), "holds bytes") {
		t.Errorf("blob edit: %v", err)
	}

	if _, err := c.CreateTable(ctx, svc.ID, db, "main", "logs", []NewColumn{{Name: "id", Type: "INTEGER", PrimaryKey: true}}, true); err != nil {
		t.Errorf("create table: %v", err)
	}
	if _, err := c.DropTable(ctx, svc.ID, db, "main", "logs", true); err != nil {
		t.Errorf("drop table: %v", err)
	}

	// The helper wrote as the file's owner: nothing beside it is root's.
	h := c.files.helpers[helperKey(svc.ID, true)]
	var out bytes.Buffer
	if err := c.helperExec(ctx, svc, false, docker.ExecOptions{Cmd: []string{"find", "/v/data/db", "!", "-user", "1000"}}, func(r io.Reader) error {
		_, err := out.ReadFrom(r)
		return err
	}); err != nil || out.Len() != 0 || h == nil {
		t.Errorf("files not the owner's: %q %v", out.String(), err)
	}

	// A helper removed behind the manager's back is started again.
	if err := c.dockerFor(svc.ServerID).RemoveContainerAndVolumes(ctx, h.id); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PgTables(ctx, svc.ID, db); err != nil {
		t.Errorf("after the helper went away: %v", err)
	}

	for _, bad := range []string{"data", "data/../x.db", "@container/app.db", "data/db/missing.db"} {
		if _, err := c.PgTables(ctx, svc.ID, bad); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
}
