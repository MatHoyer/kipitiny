package core

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestComposeDatabaseEnv(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	src := `
services:
  db:
    image: postgres:17
    environment: {POSTGRES_USER: shop, POSTGRES_DB: shop_prod, POSTGRES_PASSWORD: s3cret}
    configs: [{source: init, target: /docker-entrypoint-initdb.d/init.sql}]
  web:
    image: a
    deploy: {resources: {limits: {memory: 512m}, reservations: {memory: 128m}}}
configs:
  init: {content: "CREATE EXTENSION citext;"}
`
	if _, err := c.ApplyCompose(ctx, p.ID, []byte(src), ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	svcs, _ := c.store.ListServices(ctx, p.ID)
	i := slices.IndexFunc(svcs, func(s store.Service) bool { return s.Name == "db" })
	db := svcs[i]
	if db.Env[pgUser] != "shop" || db.Env[pgDatabase] != "shop_prod" || db.Env[pgPassword] != "s3cret" || len(db.Files) != 1 {
		t.Errorf("db env = %v, files %+v", db.Env, db.Files)
	}
	web := svcs[1-i]
	if spec := containerSpec(p, web, envSources{}, "D1", 1, route{}); spec.HostConfig.MemoryReservation != 128<<20 {
		t.Errorf("reservation = %d", spec.HostConfig.MemoryReservation)
	}

	// The names are fixed; a changed init script doesn't redeploy.
	changed := strings.NewReplacer("shop_prod", "other", "citext", "hstore").Replace(src)
	a, err := c.applyCompose(ctx, p.ID, []byte(changed), ApplyOptions{DryRun: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(a.plan.Warnings, func(w string) bool { return strings.Contains(w, "POSTGRES_DB is fixed at creation") }) {
		t.Errorf("warnings = %v", a.plan.Warnings)
	}
	if len(a.updates) != 1 || a.updates[0].deploy {
		t.Errorf("updates = %+v", a.updates)
	}

	bad := strings.Replace(src, "shop_prod", "shop-prod", 1)
	p2, _ := c.store.CreateProject(ctx, store.Project{Name: "other", ServerID: store.LocalServerID})
	if _, err := c.ApplyCompose(ctx, p2.ID, []byte(bad), ApplyOptions{DryRun: true}); err == nil || !strings.Contains(err.Error(), "POSTGRES_DB") {
		t.Errorf("bad name: %v", err)
	}
}
