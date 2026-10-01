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

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func TestTools(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	dc, _ := docker.New()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := core.New(config.Config{DataDir: t.TempDir()}, st, dc, log)
	a := api.New(c, log)
	srv := httptest.NewServer(a.Authenticated(Handler(c, "test")))
	defer srv.Close()

	// Seed a project and a database service without touching Docker.
	p, _ := st.CreateProject(ctx, store.Project{Name: "shop"})
	if _, err := st.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "db", Kind: store.ServiceKindPostgres,
		Image: "postgres:17", Replicas: 1, Env: map[string]string{"POSTGRES_PASSWORD": "hunter2"},
		Secrets: []string{"POSTGRES_PASSWORD"}}); err != nil {
		t.Fatal(err)
	}
	readTok, _ := c.CreateAPIToken(ctx, "agent", store.ScopeRead)

	connect := func(tok string) *mcp.ClientSession {
		client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
		cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{
			Endpoint: srv.URL, HTTPClient: &http.Client{Transport: bearer{tok}},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return cs
	}
	cs := connect(readTok.Token)
	defer cs.Close()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	for _, want := range []string{"list_services", "get_app_status", "get_logs", "deploy", "deploy_from_git", "rollback", "backup_database", "list_backups", "restore_database"} {
		if !strings.Contains(strings.Join(names, ","), want) {
			t.Errorf("tool %s missing: %v", want, names)
		}
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_services"})
	if err != nil || res.IsError {
		t.Fatalf("list_services: %v %+v", err, res)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(raw), `"service":"db"`) {
		t.Fatalf("list_services content: %s", raw)
	}

	// Secrets are masked in tool output.
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_app_status", Arguments: map[string]any{"service": "shop/db"}})
	if err == nil && !res.IsError {
		raw, _ = json.Marshal(res.StructuredContent)
		if strings.Contains(string(raw), "hunter2") {
			t.Fatal("secret leaked through get_app_status")
		}
	}

	// A read token can't deploy, and the refusal is a tool error, not a crash.
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "backup_database", Arguments: map[string]any{"database": "shop/db"}})
	if err != nil || !res.IsError {
		t.Fatalf("backup with read token should be refused: %v %+v", err, res)
	}
	text, _ := json.Marshal(res.Content)
	if !strings.Contains(string(text), "needs the deploy scope") {
		t.Fatalf("refusal message: %s", text)
	}

	// Unauthenticated connections are rejected.
	client := mcp.NewClient(&mcp.Implementation{Name: "anon"}, nil)
	if _, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL}, nil); err == nil {
		t.Fatal("anonymous MCP connection accepted")
	}
}
