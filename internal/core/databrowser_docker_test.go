package core

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// dataTestDB starts a database container for svc against the local Docker
// daemon and returns a core and an admin context to browse it.
//
//	KIPITINY_TEST_DOCKER=1 go test ./internal/core/ -run DataBrowserDocker
func dataTestDB(t *testing.T, kind store.ServiceKind, cfg *container.Config) (*Core, context.Context, store.Service, func(string) string) {
	t.Helper()
	if os.Getenv("KIPITINY_TEST_DOCKER") == "" {
		t.Skip("KIPITINY_TEST_DOCKER not set")
	}
	ctx := context.Background()
	dk, err := docker.New()
	if err != nil {
		t.Fatal(err)
	}
	c := newTestCore(t, config.Config{DataDir: t.TempDir()})
	c.pool = docker.NewPool(dk, c.connectServer)
	p, err := c.store.CreateProject(ctx, store.Project{Name: "datatest", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	svc := store.Service{ProjectID: p.ID, Name: "db", Kind: kind, Replicas: 1, Env: map[string]string{}}
	for _, e := range cfg.Env {
		k, v, _ := strings.Cut(e, "=")
		svc.Env[k] = v
	}
	if svc, err = c.store.CreateService(ctx, svc); err != nil {
		t.Fatal(err)
	}
	// Not kipitiny.managed: a manager running on this daemon would remove
	// it as an orphan.
	cfg.Labels = map[string]string{docker.LabelProject: p.ID, docker.LabelService: svc.ID}
	id, err := dk.Run(ctx, client.ContainerCreateOptions{Name: "kipitiny-test-" + strings.ToLower(svc.ID), Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dk.RemoveContainerAndVolumes(context.Background(), id) })
	exec := func(cmd string) string {
		t.Helper()
		var out strings.Builder
		var err error
		for range 30 { // wait for the server to accept connections
			out.Reset()
			if err = dk.Exec(ctx, id, docker.ExecOptions{Cmd: []string{"sh", "-c", cmd}, Stdout: &out}); err == nil {
				return strings.TrimSpace(out.String())
			}
			time.Sleep(time.Second)
		}
		t.Fatalf("%s: %v", cmd, err)
		return ""
	}
	return c, WithActor(ctx, Actor{Kind: "user", Name: "test", Scope: store.ScopeAdmin}), svc, exec
}

func TestDataBrowserDockerPostgres(t *testing.T) {
	c, ctx, svc, exec := dataTestDB(t, store.ServiceKindPostgres, &container.Config{
		Image: DefaultPostgresImage,
		Env:   []string{"POSTGRES_USER=app", "POSTGRES_DB=app", "POSTGRES_PASSWORD=secret"},
	})
	exec(`psql -U app -d app -v ON_ERROR_STOP=1 -c "
CREATE SCHEMA s;
CREATE TABLE s.items (id int PRIMARY KEY, name text, blob bytea, doc jsonb);
INSERT INTO s.items SELECT i, 'item ' || i, NULL, jsonb_build_object('i', i) FROM generate_series(1, 250) i;
UPDATE s.items SET name = E'multi\nline', blob = '\xdead' WHERE id = 1;
UPDATE s.items SET name = repeat('x', 5000) WHERE id = 2;
CREATE VIEW s.v AS SELECT id FROM s.items;"`)

	tables, err := c.PgTables(ctx, svc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 2 || tables[0].Name != "items" || tables[0].Kind != "table" || tables[0].Bytes == 0 || tables[1].Kind != "view" {
		t.Fatalf("tables: %+v", tables)
	}

	rows, err := c.PgRows(ctx, svc.ID, "s", "items", RowQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Columns) != 4 || !rows.Columns[0].PrimaryKey || rows.Columns[2].Type != "bytea" {
		t.Errorf("columns: %+v", rows.Columns)
	}
	if len(rows.Rows) != DataPageMax || !rows.HasMore {
		t.Fatalf("page: %d rows, more %v", len(rows.Rows), rows.HasMore)
	}
	r0 := rows.Rows[0]
	if *r0[1] != "multi\nline" || *r0[2] != `\xdead` || rows.Rows[3][2] != nil || *r0[3] != `{"i": 1}` {
		t.Errorf("row 1: %q %q %v %q", *r0[1], *r0[2], rows.Rows[3][2], *r0[3])
	}
	if len(rows.Truncated) != 1 || rows.Truncated[0] != [2]int{1, 1} || len(*rows.Rows[1][1]) != DataCellMax {
		t.Errorf("truncated: %v", rows.Truncated)
	}

	rows, err = c.PgRows(ctx, svc.ID, "s", "items", RowQuery{Limit: 5, OrderBy: "id", Desc: true,
		Filters: []PgFilter{{Column: "id", Op: "<", Value: "100"}, {Column: "name", Op: "like", Value: "item 9%"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Rows) != 5 || *rows.Rows[0][0] != "99" || !rows.HasMore {
		t.Errorf("filtered: %d rows, first %s", len(rows.Rows), *rows.Rows[0][0])
	}

	_, err = c.PgRows(ctx, svc.ID, "s", "items", RowQuery{Filters: []PgFilter{{Column: "id", Op: "=", Value: "abc"}}})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "invalid input syntax") {
		t.Errorf("bad value: %v", err)
	}
	if _, err = c.PgRows(ctx, svc.ID, "s", "missing", RowQuery{}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing table: %v", err)
	}

	var csv strings.Builder
	if err := c.PgExport(ctx, svc.ID, "s", "items", RowQuery{Filters: []PgFilter{{Column: "id", Op: "<=", Value: "3"}}}, &csv); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(csv.String()), "\n"); lines[0] != "id,name,blob,doc" || len(lines) != 5 {
		t.Errorf("export:\n%s", csv.String())
	}

	if _, err := c.PgTables(WithActor(context.Background(), Actor{Scope: store.ScopeRead}), svc.ID); err != nil {
		t.Errorf("read scope: %v", err)
	}
	if _, err := c.PgTables(context.Background(), svc.ID); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("no actor: %v", err)
	}
}
