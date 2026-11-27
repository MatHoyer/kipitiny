package data

import (
	"errors"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestRowChangesDockerPostgres(t *testing.T) {
	c, ctx, svc, exec := dataTestDB(t, domain.ServiceKindPostgres, &domain.ContainerSpec{
		Image: DefaultPostgresImage,
		Env:   []string{"POSTGRES_USER=app", "POSTGRES_DB=app", "POSTGRES_PASSWORD=secret"},
	})
	exec(`psql -U app -d app -v ON_ERROR_STOP=1 -c "
CREATE TABLE items (id serial PRIMARY KEY, name text NOT NULL DEFAULT 'none', found boolean, doc jsonb);
INSERT INTO items (name, found, doc) VALUES ('a', true, '{\"k\": 1}'), ('b', NULL, NULL), ('c', false, NULL);
CREATE VIEW v AS SELECT id FROM items;"`)
	s := func(v string) *string { return &v }
	query := func(sql string) string { return exec(`psql -U app -d app -At -c "` + sql + `"`) }

	ch := RowChanges{
		Deletes: []RowDelete{{Key: map[string]string{"id": "3"}}},
		Updates: []RowUpdate{{Key: map[string]string{"id": "1"}, Set: map[string]*string{"name": s("it's\nnew"), "found": nil, "doc": s(`{"k": 2}`)},
			Was: map[string]*string{"name": s("a"), "found": s("true"), "doc": s(`{"k": 1}`)}}},
		Inserts: []map[string]*string{{"found": s("true")}},
	}
	res, err := c.ApplyRowChanges(ctx, svc.ID, "", "public", "items", ch, false)
	if err != nil || res.Applied || len(res.Statements) != 3 {
		t.Fatalf("preview = %+v, %v", res, err)
	}
	if got := query("SELECT count(*) FROM items"); got != "3" {
		t.Fatalf("preview changed rows: %s", got)
	}
	if res, err = c.ApplyRowChanges(ctx, svc.ID, "", "public", "items", ch, true); err != nil || !res.Applied {
		t.Fatalf("apply = %+v, %v", res, err)
	}
	if got := query("SELECT id, name, found IS NULL, doc FROM items ORDER BY id"); got != "1|it's\nnew|t|{\"k\": 2}\n2|b|t|\n4|none|f|" {
		t.Errorf("rows after save:\n%s", got)
	}

	// The row changed since it was loaded: nothing of the save stays.
	stale := RowChanges{
		Deletes: []RowDelete{{Key: map[string]string{"id": "2"}}},
		Updates: []RowUpdate{{Key: map[string]string{"id": "1"}, Set: map[string]*string{"name": s("x")}, Was: map[string]*string{"name": s("a")}}},
	}
	_, err = c.ApplyRowChanges(ctx, svc.ID, "", "public", "items", stale, true)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "row id 1 changed or was deleted") || strings.Contains(err.Error(), "CONTEXT") {
		t.Errorf("stale update: %v", err)
	}
	if got := query("SELECT count(*) FROM items WHERE id = 2"); got != "1" {
		t.Errorf("stale save kept its delete")
	}
	_, err = c.ApplyRowChanges(ctx, svc.ID, "", "public", "items", RowChanges{Inserts: []map[string]*string{{"name": nil}}}, true)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "not-null") {
		t.Errorf("NULL in NOT NULL: %v", err)
	}
	_, err = c.ApplyRowChanges(ctx, svc.ID, "", "public", "v", RowChanges{Inserts: []map[string]*string{{}}}, true)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "view") {
		t.Errorf("view: %v", err)
	}
	if _, err = c.ApplyRowChanges(ctx, svc.ID, "", "public", "missing", RowChanges{Inserts: []map[string]*string{{}}}, true); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing table: %v", err)
	}
	read := identity.WithActor(ctx, identity.Actor{Kind: "token", Name: "read"})
	if _, err = c.ApplyRowChanges(read, svc.ID, "", "public", "items", ch, false); err == nil {
		t.Error("a stranger previewed changes")
	}
}

func TestKeyChangesDockerRedis(t *testing.T) {
	c, ctx, svc, exec := dataTestDB(t, domain.ServiceKindRedis, &domain.ContainerSpec{
		Image: DefaultRedisImage,
		Env:   []string{"REDIS_PASSWORD=secret", "REDISCLI_AUTH=secret"},
		Cmd:   []string{"redis-server", "--requirepass", "secret"},
	})
	exec(`redis-cli SET greeting hello EX 3600 && redis-cli HSET user:1 name Ada lang en && redis-cli RPUSH queue a b c d e &&
redis-cli SADD tags x y && redis-cli ZADD scores 1 low 2.5 mid && redis-cli XADD events 1-1 k v1 && redis-cli XADD events 2-1 k v2`)
	apply := func(key string, ch KeyChanges) (ChangeResult, error) {
		return c.ApplyKeyChanges(ctx, svc.ID, key, ch, true)
	}

	res, err := c.ApplyKeyChanges(ctx, svc.ID, "greeting", KeyChanges{Type: "string", Edits: []ItemEdit{{Item: []string{"hello"}, Set: []string{"hi \"you\"\n"}}}}, false)
	if err != nil || res.Applied || res.Statements[0] != `SET greeting "hi \"you\"\n" KEEPTTL` {
		t.Fatalf("preview = %#v, %v", res, err)
	}
	if _, err := apply("greeting", KeyChanges{Type: "string", Edits: []ItemEdit{{Item: []string{"hello"}, Set: []string{"hi"}}}}); err != nil {
		t.Fatal(err)
	}
	if got := exec(`redis-cli GET greeting && redis-cli TTL greeting`); !strings.HasPrefix(got, "hi\n") || strings.HasSuffix(got, "-1") {
		t.Errorf("string: %q", got)
	}
	_, err = apply("greeting", KeyChanges{Type: "string", Edits: []ItemEdit{{Item: []string{"hello"}, Set: []string{"x"}}}})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "the value changed or was deleted since it was loaded") {
		t.Errorf("stale string: %v", err)
	}

	if _, err := apply("user:1", KeyChanges{Type: "hash", Edits: []ItemEdit{{Item: []string{"name", "Ada"}, Set: []string{"name", "Grace"}}},
		Deletes: [][]string{{"lang", "en"}}, Inserts: [][]string{{"born", "1906"}}}); err != nil {
		t.Fatal(err)
	}
	if got := exec(`redis-cli HGETALL user:1`); got != "name\nGrace\nborn\n1906" {
		t.Errorf("hash: %q", got)
	}
	// Indexes are as loaded: deleting 1 and 3 removes b and d, whatever the order.
	if _, err := apply("queue", KeyChanges{Type: "list", Edits: []ItemEdit{{Item: []string{"0", "a"}, Set: []string{"0", "A"}}},
		Deletes: [][]string{{"1", "b"}, {"3", "d"}}, Inserts: [][]string{{"", "f"}}}); err != nil {
		t.Fatal(err)
	}
	if got := exec(`redis-cli LRANGE queue 0 -1`); got != "A\nc\ne\nf" {
		t.Errorf("list: %q", got)
	}
	if _, err := apply("tags", KeyChanges{Type: "set", Edits: []ItemEdit{{Item: []string{"x"}, Set: []string{"z"}}}, Inserts: [][]string{{"w"}}}); err != nil {
		t.Fatal(err)
	}
	if got := exec(`redis-cli SMEMBERS tags | sort`); got != "w\ny\nz" {
		t.Errorf("set: %q", got)
	}
	if _, err := apply("scores", KeyChanges{Type: "zset", Edits: []ItemEdit{{Item: []string{"mid", "2.5"}, Set: []string{"mid", "7"}}, {Item: []string{"low", "1"}, Set: []string{"bottom", "0"}}},
		Inserts: [][]string{{"top", "9"}}}); err != nil {
		t.Fatal(err)
	}
	if got := exec(`redis-cli ZRANGE scores 0 -1 WITHSCORES`); got != "bottom\n0\nmid\n7\ntop\n9" {
		t.Errorf("zset: %q", got)
	}
	// A failed check writes nothing, even the changes before it.
	_, err = apply("scores", KeyChanges{Type: "zset", Deletes: [][]string{{"top", "9"}, {"mid", "1"}}})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), `member "mid" changed`) {
		t.Errorf("stale zset: %v", err)
	}
	if got := exec(`redis-cli ZCARD scores`); got != "3" {
		t.Errorf("stale save deleted: %s", got)
	}
	if _, err = apply("scores", KeyChanges{Type: "zset", Inserts: [][]string{{"top", "1"}}}); err == nil || !strings.Contains(err.Error(), `member "top" already exists`) {
		t.Errorf("taken member: %v", err)
	}
	if _, err := apply("events", KeyChanges{Type: "stream", Deletes: [][]string{{"1-1", "k", "v1"}}}); err != nil {
		t.Fatal(err)
	}
	if got := exec(`redis-cli XLEN events`); got != "1" {
		t.Errorf("stream: %s", got)
	}
	if _, err := apply("tags", KeyChanges{Type: "hash", Delete: true}); !errors.Is(err, ErrInvalid) {
		t.Errorf("wrong type: %v", err)
	}
	if _, err := apply("tags", KeyChanges{Type: "set", Delete: true}); err != nil {
		t.Fatal(err)
	}
	if got := exec(`redis-cli EXISTS tags`); got != "0" {
		t.Errorf("key kept")
	}
	if _, err := apply("new:h", KeyChanges{Type: "hash", Create: true, Inserts: [][]string{{"a", "1"}, {"b", "2"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := apply("new:s", KeyChanges{Type: "string", Create: true, Inserts: [][]string{{"v"}}}); err != nil {
		t.Fatal(err)
	}
	if got := exec(`redis-cli HGETALL new:h && redis-cli GET new:s && redis-cli TTL new:s`); got != "a\n1\nb\n2\nv\n-1" {
		t.Errorf("created: %q", got)
	}
	if _, err = apply("new:s", KeyChanges{Type: "list", Create: true, Inserts: [][]string{{"", "x"}}}); err == nil || !strings.Contains(err.Error(), `key "new:s" already exists`) {
		t.Errorf("existing key: %v", err)
	}
	for name, ch := range map[string]KeyChanges{
		"create empty":  {Type: "set", Create: true},
		"create+delete": {Type: "set", Create: true, Delete: true, Inserts: [][]string{{"a"}}},
		"empty":         {Type: "hash"},
		"delete+edit":   {Type: "set", Delete: true, Inserts: [][]string{{"a"}}},
		"rename field":  {Type: "hash", Edits: []ItemEdit{{Item: []string{"a", "1"}, Set: []string{"b", "1"}}}},
		"bad score":     {Type: "zset", Inserts: [][]string{{"a", "x"}}},
		"stream insert": {Type: "stream", Inserts: [][]string{{"*", "k", "v"}}},
		"short item":    {Type: "hash", Inserts: [][]string{{"a"}}},
	} {
		if _, err := c.ApplyKeyChanges(ctx, svc.ID, "k", ch, false); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
