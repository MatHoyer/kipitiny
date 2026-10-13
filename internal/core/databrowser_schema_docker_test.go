package core

import (
	"errors"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"

	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestSchemaDockerPostgres(t *testing.T) {
	c, ctx, svc, exec := dataTestDB(t, store.ServiceKindPostgres, &container.Config{
		Image: DefaultPostgresImage,
		Env:   []string{"POSTGRES_USER=app", "POSTGRES_DB=app", "POSTGRES_PASSWORD=secret"},
	})
	cols := []NewColumn{{Name: "id", Type: "bigserial", PrimaryKey: true}, {Name: "name", Type: "text", Default: "'x'"}}
	res, err := c.CreateTable(ctx, svc.ID, "", "", "items", cols, false)
	if err != nil || res.Applied || !strings.HasPrefix(res.Statements[0], `CREATE TABLE "public"."items" (`) {
		t.Fatalf("preview = %+v, %v", res, err)
	}
	if tables, _ := c.PgTables(ctx, svc.ID, ""); len(tables) != 0 {
		t.Fatalf("preview created: %+v", tables)
	}
	if _, err := c.CreateTable(ctx, svc.ID, "", "", "items", cols, true); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ApplyRowChanges(ctx, svc.ID, "", "public", "items", RowChanges{Inserts: []map[string]*string{{}}}, true); err != nil {
		t.Fatal(err)
	}
	if got := exec(`psql -U app -d app -At -c "SELECT id, name FROM items"`); got != "1|x" {
		t.Errorf("row: %q", got)
	}
	if _, err := c.CreateTable(ctx, svc.ID, "", "", "items", cols, true); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("twice: %v", err)
	}
	exec(`psql -U app -d app -c "CREATE VIEW v AS SELECT id FROM items"`)
	// The view depends on the table: no cascade.
	if _, err := c.DropTable(ctx, svc.ID, "", "public", "items", true); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "depend") {
		t.Errorf("drop with a view on it: %v", err)
	}
	if res, err := c.DropTable(ctx, svc.ID, "", "public", "v", true); err != nil || res.Statements[0] != `DROP VIEW "public"."v";` {
		t.Fatalf("drop view = %+v, %v", res, err)
	}
	if _, err := c.DropTable(ctx, svc.ID, "", "public", "items", true); err != nil {
		t.Fatal(err)
	}
	if tables, _ := c.PgTables(ctx, svc.ID, ""); len(tables) != 0 {
		t.Errorf("left: %+v", tables)
	}
	if _, err := c.DropTable(ctx, svc.ID, "", "public", "items", false); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
}

func TestSchemaDockerMySQL(t *testing.T) {
	c, ctx, svc, _ := dumpTestDB(t, store.ServiceKindMariaDB)
	ctx = WithActor(ctx, Actor{Kind: "user", Name: "test", Role: store.RoleAdmin, Scope: store.ScopeAdmin})
	cols := []NewColumn{{Name: "id", Type: "bigint auto_increment", PrimaryKey: true}, {Name: "at", Type: "datetime", Default: "CURRENT_TIMESTAMP"}}
	if _, err := c.CreateTable(ctx, svc.ID, "", "", "items", cols, true); err != nil {
		t.Fatal(err)
	}
	tables, err := c.PgTables(ctx, svc.ID, "")
	if err != nil || len(tables) != 1 || !tables[0].Columns[0].PrimaryKey {
		t.Fatalf("tables = %+v, %v", tables, err)
	}
	if res, err := c.DropTable(ctx, svc.ID, "", "app", "items", true); err != nil || res.Statements[0] != "DROP TABLE `items`;" {
		t.Fatalf("drop = %+v, %v", res, err)
	}
}

func TestSchemaDockerMongo(t *testing.T) {
	c, ctx, svc, _ := dumpTestDB(t, store.ServiceKindMongoDB)
	ctx = WithActor(ctx, Actor{Kind: "user", Name: "test", Role: store.RoleAdmin, Scope: store.ScopeAdmin})
	if _, err := c.CreateCollection(ctx, svc.ID, "", "events", true); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateCollection(ctx, svc.ID, "", "events", true); !errors.Is(err, ErrInvalid) {
		t.Errorf("twice: %v", err)
	}
	if _, err := c.CreateCollection(ctx, svc.ID, "", "system.x", false); !errors.Is(err, ErrInvalid) {
		t.Errorf("system: %v", err)
	}
	if res, err := c.DropCollection(ctx, svc.ID, "", "events", true); err != nil || res.Statements[0] != "db.events.drop()" {
		t.Fatalf("drop = %+v, %v", res, err)
	}
	if tables, _ := c.PgTables(ctx, svc.ID, ""); len(tables) != 0 {
		t.Errorf("left: %+v", tables)
	}
}
