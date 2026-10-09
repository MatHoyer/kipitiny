package core

import (
	"context"
	"errors"
	"os"
	"slices"
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
	if len(tables) != 2 || tables[0].Name != "items" || tables[0].Kind != "table" || tables[0].Bytes == 0 || tables[1].Kind != "view" ||
		len(tables[0].Columns) != 4 || !tables[0].Columns[0].PrimaryKey || tables[0].Columns[3].Type != "jsonb" || len(tables[1].Columns) != 1 {
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

	rows, err = c.PgRows(ctx, svc.ID, "s", "items", RowQuery{Search: "ITEM 24", Filters: []PgFilter{{Column: "id", Op: ">", Value: "240"}}})
	if err != nil || len(rows.Rows) != 9 || rows.HasMore {
		t.Errorf("search: %d rows, %v", len(rows.Rows), err)
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

func TestDataBrowserDockerRedis(t *testing.T) {
	c, ctx, svc, exec := dataTestDB(t, store.ServiceKindRedis, &container.Config{
		Image: DefaultRedisImage,
		Env:   []string{"REDIS_PASSWORD=secret", "REDISCLI_AUTH=secret"},
		Cmd:   []string{"redis-server", "--requirepass", "secret"},
	})
	exec(`redis-cli SET greeting hello && redis-cli SET big "$(head -c 5000 /dev/zero | tr '\0' x)" &&
redis-cli HSET user:1 name Ada lang en && redis-cli RPUSH queue a b c d e && redis-cli SADD tags x y &&
redis-cli ZADD scores 1 low 2 mid 3 high && redis-cli XADD events 1-1 k v1 && redis-cli XADD events 2-1 k v2 &&
redis-cli SET user:2 Bob EX 3600 && redis-cli SET "odd
key" v`)

	var all []RedisKey
	cursor := "0"
	for {
		page, err := c.RedisScan(ctx, svc.ID, cursor, "", 3)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, page.Keys...)
		if cursor = page.Cursor; cursor == "0" {
			break
		}
	}
	if len(all) != 9 {
		t.Fatalf("scan: %d keys: %+v", len(all), all)
	}
	users, err := c.RedisScan(ctx, svc.ID, "0", "user:*", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(users.Keys) != 2 || users.Cursor != "0" {
		t.Errorf("pattern: %+v", users)
	}
	for _, k := range users.Keys {
		if k.Key == "user:2" && (k.TTL <= 0 || k.Type != "string" || k.Bytes <= 0) {
			t.Errorf("user:2: %+v", k)
		}
	}

	get := func(key, cursor string, count int) RedisValue {
		t.Helper()
		v, err := c.RedisGet(ctx, svc.ID, key, cursor, count)
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		return v
	}
	if v := get("greeting", "", 0); v.Items[0][0] != "hello" || v.Length != 5 || v.TTL != -1 {
		t.Errorf("string: %+v", v)
	}
	if v := get("big", "", 0); v.Length != 5000 || len(v.Items[0][0]) != DataCellMax || len(v.Truncated) != 1 {
		t.Errorf("big string: len %d, truncated %v", v.Length, v.Truncated)
	}
	if v := get("odd\nkey", "", 0); v.Items[0][0] != "v" {
		t.Errorf("odd key: %+v", v)
	}
	if v := get("user:1", "", 0); v.Type != "hash" || v.Length != 2 || len(v.Items) != 2 || v.Cursor != "" {
		t.Errorf("hash: %+v", v)
	}
	if v := get("tags", "", 0); v.Type != "set" || len(v.Items) != 2 {
		t.Errorf("set: %+v", v)
	}
	v := get("queue", "", 2)
	if v.Length != 5 || len(v.Items) != 2 || v.Items[1][0] != "1" || v.Items[1][1] != "b" || v.Cursor != "2" {
		t.Errorf("list page 1: %+v", v)
	}
	if v = get("queue", "4", 2); len(v.Items) != 1 || v.Items[0][1] != "e" || v.Cursor != "" {
		t.Errorf("list last page: %+v", v)
	}
	if v = get("scores", "1", 10); v.Type != "zset" || len(v.Items) != 2 || v.Items[0][0] != "mid" || v.Items[0][1] != "2" {
		t.Errorf("zset: %+v", v)
	}
	v = get("events", "", 1)
	if v.Type != "stream" || v.Length != 2 || v.Items[0][0] != "1-1" || v.Items[0][2] != "v1" || v.Cursor != "1-1" {
		t.Errorf("stream page 1: %+v", v)
	}
	if v = get("events", v.Cursor, 1); len(v.Items) != 1 || v.Items[0][0] != "2-1" {
		t.Errorf("stream page 2: %+v", v)
	}
	if _, err := c.RedisGet(ctx, svc.ID, "missing", "", 0); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing key: %v", err)
	}
	if _, err := c.RedisGet(ctx, svc.ID, "queue", "1; FLUSHALL", 0); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad cursor: %v", err)
	}
}

func TestDataConsoleDockerPostgres(t *testing.T) {
	c, ctx, svc, exec := dataTestDB(t, store.ServiceKindPostgres, &container.Config{
		Image: DefaultPostgresImage,
		Env:   []string{"POSTGRES_USER=app", "POSTGRES_DB=app", "POSTGRES_PASSWORD=secret"},
	})
	exec(`psql -U app -d app -c "CREATE TABLE t (id int, name text); INSERT INTO t SELECT i, 'n' || i FROM generate_series(1, 300) i; INSERT INTO t VALUES (0, NULL), (-1, '')"`)

	res, err := c.DataConsole(ctx, svc.ID, "SELECT 1; SELECT id, name FROM t WHERE id <= 0 ORDER BY id", false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Columns, []string{"id", "name"}) || len(res.Rows) != 2 || *res.Rows[0][1] != "" || res.Rows[1][1] != nil {
		t.Errorf("select: %+v", res)
	}
	if res, err = c.DataConsole(ctx, svc.ID, "SELECT * FROM t", false); err != nil || len(res.Rows) != DataPageMax || !res.More {
		t.Errorf("capped select: %d rows, more %v, %v", len(res.Rows), res.More, err)
	}
	if _, err = c.DataConsole(ctx, svc.ID, "DELETE FROM t", false); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("read-only delete: %v", err)
	}
	if _, err = c.DataConsole(ctx, svc.ID, "SELECT nope", false); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "nope") {
		t.Errorf("bad sql: %v", err)
	}
	if res, err = c.DataConsole(ctx, svc.ID, "DELETE FROM t WHERE id < 0", true); err != nil || res.Output != "DELETE 1" {
		t.Errorf("write: %+v %v", res, err)
	}
	// A failing statement rolls the whole run back.
	if _, err = c.DataConsole(ctx, svc.ID, "DELETE FROM t; SELECT nope", true); err == nil {
		t.Error("failing write succeeded")
	}
	// Past the caps a write still runs to its end.
	if res, err = c.DataConsole(ctx, svc.ID, "UPDATE t SET name = 'x' RETURNING *", true); err != nil || !res.More {
		t.Errorf("returning: more %v %v", res.More, err)
	}
	if got := exec(`psql -U app -d app -tAc "SELECT count(*) FILTER (WHERE name = 'x'), count(*) FROM t"`); got != "301|301" {
		t.Errorf("after writes: %s", got)
	}

	audit, err := c.store.ListAudit(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) != 7 || audit[0].Action != "data console (write)" || audit[0].Target != svc.ID {
		t.Errorf("audit: %+v", audit)
	}
	if _, err := c.DataConsole(WithActor(context.Background(), Actor{Scope: store.ScopeRead}), svc.ID, "SELECT 1", false); !errors.Is(err, ErrForbidden) {
		t.Errorf("read scope: %v", err)
	}
}

func TestDataConsoleDockerRedis(t *testing.T) {
	c, ctx, svc, exec := dataTestDB(t, store.ServiceKindRedis, &container.Config{
		Image: DefaultRedisImage,
		Env:   []string{"REDIS_PASSWORD=secret", "REDISCLI_AUTH=secret"},
		Cmd:   []string{"redis-server", "--requirepass", "secret"},
	})
	exec(`redis-cli SET greeting hello`)
	run := func(cmd string, write bool) (string, error) {
		res, err := c.DataConsole(ctx, svc.ID, cmd, write)
		return res.Output, err
	}
	if out, err := run("GET greeting", false); err != nil || out != `"hello"` {
		t.Errorf("get: %q %v", out, err)
	}
	if out, err := run("CONFIG GET maxmemory", false); !errors.Is(err, ErrInvalid) {
		t.Errorf("read-only config: %q %v", out, err)
	}
	if _, err := run(`SET greeting "hi there"`, false); !errors.Is(err, ErrInvalid) {
		t.Errorf("read-only set: %v", err)
	}
	if out, err := run(`SET greeting "hi there"`, true); err != nil || out != "OK" {
		t.Errorf("write set: %q %v", out, err)
	}
	if out, _ := run("GET greeting", false); out != `"hi there"` {
		t.Errorf("after set: %q", out)
	}
	if _, err := run("LPUSH greeting x", true); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "WRONGTYPE") {
		t.Errorf("error reply: %v", err)
	}
	for _, cmd := range []string{"MONITOR", "blpop q 0", "XREAD BLOCK 0 STREAMS s $"} {
		if _, err := run(cmd, true); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", cmd, err)
		}
	}
}
