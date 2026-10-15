package mcp

import (
	"context"
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

func TestDryRunWrapper(t *testing.T) {
	ctx := context.Background()
	type planIn struct {
		DryRun bool `json:"dry_run"`
	}
	type queryIn struct {
		Query string `json:"query"`
		Write bool   `json:"write"`
	}
	type out struct{ Ran bool }

	plan := dryRun("apply", func(_ context.Context, _ *mcp.CallToolRequest, in planIn) (*mcp.CallToolResult, out, error) {
		return nil, out{Ran: in.DryRun}, nil
	})
	if _, o, err := plan(ctx, nil, planIn{}); err != nil || !o.Ran {
		t.Errorf("a tool taking dry_run returns its plan: %+v %v", o, err)
	}

	ran := false
	query := dryRun("query_database", func(_ context.Context, _ *mcp.CallToolRequest, in queryIn) (*mcp.CallToolResult, out, error) {
		ran = true
		return nil, out{Ran: true}, nil
	})
	if _, _, err := query(ctx, nil, queryIn{Query: "select 1"}); err != nil || !ran {
		t.Errorf("a read-only query runs: %v", err)
	}
	ran = false
	if _, _, err := query(ctx, nil, queryIn{Query: "drop table x", Write: true}); err == nil || ran ||
		!strings.Contains(err.Error(), "dry run") || !strings.Contains(err.Error(), "drop table x") {
		t.Errorf("a writing query doesn't run: %v", err)
	}
}

func TestDryRunMode(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	dc, _ := docker.New()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := core.New(config.Config{DataDir: t.TempDir()}, st, dc, log)
	srv := httptest.NewServer(api.New(c, log).Authenticated(Handler(c, "test")))
	defer srv.Close()
	p, err := st.CreateProject(ctx, store.Project{Name: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	admin, _ := asUser(t, c, st, "admin", store.RoleAdmin)
	tok, err := c.CreateAPIToken(admin, core.TokenInput{Name: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: srv.URL + "?dry_run=true", HTTPClient: &http.Client{Transport: bearer{tok.Token}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if !strings.Contains(cs.InitializeResult().Instructions, "dry-run mode") {
		t.Errorf("instructions don't say dry-run mode: %s", cs.InitializeResult().Instructions)
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "delete_project", Arguments: map[string]any{"project": "shop", "confirm": "shop"}})
	if err != nil || !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "dry run") {
		t.Fatalf("delete_project in dry-run mode: %v %+v", err, res)
	}
	if _, err := st.GetProject(ctx, p.ID); err != nil {
		t.Fatalf("project deleted in dry-run mode: %v", err)
	}
	if res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_audit_log"}); err != nil || res.IsError {
		t.Fatalf("read tools work in dry-run mode: %v %+v", err, res)
	}
}
