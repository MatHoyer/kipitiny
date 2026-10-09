package mcp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MatHoyer/kipitiny/internal/api"
	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
	"github.com/MatHoyer/kipitiny/internal/store/sqlite"
)

// env is an MCP server over a fresh store, for tools that refuse before
// reaching Docker.
type env struct {
	st      store.Store
	c       *core.Core
	session func(store.Scope) *mcp.ClientSession
	// call returns the structured output, or the error text and true.
	call func(cs *mcp.ClientSession, name string, args map[string]any) (string, bool)
}

func newEnv(t *testing.T) env {
	t.Helper()
	ctx := context.Background()
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	dc, _ := docker.New()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := core.New(config.Config{DataDir: t.TempDir()}, st, dc, log)
	srv := httptest.NewServer(api.New(c, log).Authenticated(Handler(c, "test")))
	t.Cleanup(srv.Close)
	e := env{st: st, c: c}
	e.session = func(scope store.Scope) *mcp.ClientSession {
		tok, _ := c.CreateAPIToken(ctx, string(scope), scope)
		cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
			Endpoint: srv.URL, HTTPClient: &http.Client{Transport: bearer{tok.Token}},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cs.Close() })
		return cs
	}
	e.call = func(cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		raw, _ := json.Marshal(res.StructuredContent)
		if res.IsError {
			raw, _ = json.Marshal(res.Content)
		}
		return string(raw), res.IsError
	}
	return e
}

func TestDataTools(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p, _ := e.st.CreateProject(ctx, store.Project{Name: "shop"})
	if _, err := e.st.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "db", Kind: store.ServiceKindPostgres, Image: "postgres:17", Replicas: 1,
		Env: map[string]string{"POSTGRES_USER": "shop", "POSTGRES_PASSWORD": "hunter2", "POSTGRES_DB": "shop"}, Secrets: []string{"POSTGRES_PASSWORD"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1}); err != nil {
		t.Fatal(err)
	}
	read, admin := e.session(store.ScopeRead), e.session(store.ScopeAdmin)

	for name, args := range map[string]map[string]any{
		"create_database": {"service": "shop/db", "name": "analytics"},
		"query_database":  {"service": "shop/db", "query": "SELECT 1"},
		"get_connection":  {"service": "shop/db"},
	} {
		if out, isErr := e.call(read, name, args); !isErr || !strings.Contains(out, "needs the admin scope") {
			t.Errorf("%s with a read token: %s", name, out)
		}
	}

	// Credentials are revealed to admin only.
	out, isErr := e.call(admin, "get_connection", map[string]any{"service": "shop/db"})
	if isErr || !strings.Contains(out, `"password":"hunter2"`) || !strings.Contains(out, `"database":"shop"`) {
		t.Errorf("get_connection: %s", out)
	}
	if out, isErr = e.call(admin, "get_connection", map[string]any{"service": "shop/web"}); !isErr || !strings.Contains(out, "not a database") {
		t.Errorf("get_connection of an app: %s", out)
	}

	// The browser checks the kind before reaching Docker.
	for name, args := range map[string]map[string]any{
		"list_databases":  {"service": "shop/web"},
		"list_tables":     {"service": "shop/web"},
		"read_table":      {"service": "shop/web", "table": "users"},
		"scan_redis_keys": {"service": "shop/db"},
		"get_redis_key":   {"service": "shop/db", "key": "k"},
	} {
		if out, isErr := e.call(read, name, args); !isErr || strings.Contains(out, "needs the") {
			t.Errorf("%s on the wrong kind: %s", name, out)
		}
	}
	if out, isErr = e.call(admin, "query_database", map[string]any{"service": "shop/db", "query": " "}); !isErr || !strings.Contains(out, "empty query") {
		t.Errorf("query_database empty: %s", out)
	}
	if out, isErr = e.call(admin, "create_database", map[string]any{"service": "shop/db", "name": "Bad-Name"}); !isErr || !strings.Contains(out, "lowercase") {
		t.Errorf("create_database bad name: %s", out)
	}
	if out, isErr = e.call(read, "list_tables", map[string]any{"service": "shop/nope"}); !isErr || !strings.Contains(out, "not found") {
		t.Errorf("list_tables unknown service: %s", out)
	}
}
