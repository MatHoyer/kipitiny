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
	for _, want := range []string{"list_projects", "list_services", "get_app_status", "get_logs", "get_deployment_log", "deploy", "deploy_image", "rollback",
		"service_action", "create_project", "rename_project", "rename_service", "get_project_git", "link_project_git", "sync_project_git",
		"backup_database", "list_backups", "list_storage", "restore_database", "list_databases", "create_database", "list_tables", "read_table",
		"scan_redis_keys", "get_redis_key", "query_database", "get_connection", "delete_service", "delete_project", "list_backup_schedules",
		"set_backup_schedule", "delete_backup_schedule", "set_uptime_check", "delete_uptime_check", "backup_project", "verify_backup",
		"delete_backup", "list_restores", "list_deployments", "list_git_repos", "list_git_branches", "get_manager_status", "get_topology",
		"get_audit_log", "list_networks", "create_network", "delete_network", "set_service_networks",
		"list_volume_files", "read_volume_file", "write_volume_file", "make_volume_dir", "move_volume_path", "delete_volume_paths"} {
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

	// With a deploy token, deploy_image only retags an existing app: no new
	// apps, other images or env (which could pull secrets out).
	if _, err := st.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp,
		Image: "ghcr.io/org/web:v1", Replicas: 1}); err != nil {
		t.Fatal(err)
	}
	deployTok, _ := c.CreateAPIToken(ctx, "ci", store.ScopeDeploy)
	ds := connect(deployTok.Token)
	defer ds.Close()
	for name, args := range map[string]map[string]any{
		"new app":     {"project": "shop", "name": "evil", "image": "alpine"},
		"other image": {"project": "shop", "name": "web", "image": "alpine"},
		"env":         {"project": "shop", "name": "web", "image": "ghcr.io/org/web:v2", "env": map[string]any{"X": "{{ db.main.PASSWORD }}"}},
		"ports":       {"project": "shop", "name": "web", "image": "ghcr.io/org/web:v2", "publishedPorts": []any{map[string]any{"hostPort": 2222}}},
	} {
		res, err := ds.CallTool(ctx, &mcp.CallToolParams{Name: "deploy_image", Arguments: args})
		if err != nil || !res.IsError {
			t.Fatalf("deploy_image %s with deploy token should be refused: %v %+v", name, err, res)
		}
		text, _ := json.Marshal(res.Content)
		if !strings.Contains(string(text), "needs the admin scope") {
			t.Fatalf("%s refusal message: %s", name, text)
		}
	}

	// Unauthenticated connections are rejected.
	client := mcp.NewClient(&mcp.Implementation{Name: "anon"}, nil)
	if _, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL}, nil); err == nil {
		t.Fatal("anonymous MCP connection accepted")
	}
}

func TestDocs(t *testing.T) {
	for _, tc := range []struct{ version, index, base, sources string }{
		{"0.10.2", "https://kipitiny.mathieuhoyer.fr/docs/0.10/llms.txt", "https://kipitiny.mathieuhoyer.fr/docs/0.10", "https://github.com/MatHoyer/kipitiny/tree/0.10.2/site/docs"},
		{"dev", "https://kipitiny.mathieuhoyer.fr/llms.txt", "https://kipitiny.mathieuhoyer.fr/docs", "https://github.com/MatHoyer/kipitiny/tree/main/site/docs"},
	} {
		index, base, sources := docs(tc.version)
		if index != tc.index || base != tc.base || sources != tc.sources {
			t.Errorf("docs(%q) = %q, %q, %q", tc.version, index, base, sources)
		}
	}
}
