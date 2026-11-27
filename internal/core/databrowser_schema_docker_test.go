package core

import (
	"errors"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/usecase/data"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestSchemaDockerMySQL(t *testing.T) {
	c, ctx, svc, _ := dumpTestDB(t, domain.ServiceKindMariaDB)
	ctx = identity.WithActor(ctx, identity.Actor{Kind: "user", Name: "test", Role: domain.RoleAdmin})
	cols := []data.NewColumn{{Name: "id", Type: "bigint auto_increment", PrimaryKey: true}, {Name: "at", Type: "datetime", Default: "CURRENT_TIMESTAMP"}}
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
	c, ctx, svc, _ := dumpTestDB(t, domain.ServiceKindMongoDB)
	ctx = identity.WithActor(ctx, identity.Actor{Kind: "user", Name: "test", Role: domain.RoleAdmin})
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
