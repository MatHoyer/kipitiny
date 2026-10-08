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

// TestProjectTools covers projects, git links, renames, service actions and
// deployment logs, without touching Docker or the network.
func TestProjectTools(t *testing.T) {
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

	shop, _ := st.CreateProject(ctx, store.Project{Name: "shop"})
	if _, err := st.CreateProject(ctx, store.Project{Name: "site"}); err != nil {
		t.Fatal(err)
	}
	web, err := st.CreateService(ctx, store.Service{ProjectID: shop.ID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveProjectGit(ctx, store.ProjectGit{ProjectID: shop.ID, RepoURL: "https://github.com/me/shop.git", Branch: "main",
		Path: "compose.yaml", AutoSync: true, PollSeconds: 300, WebhookSecret: "hook-s3cret"}); err != nil {
		t.Fatal(err)
	}

	session := func(scope store.Scope) *mcp.ClientSession {
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
	call := func(cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
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
	read, admin := session(store.ScopeRead), session(store.ScopeAdmin)

	out, isErr := call(read, "list_projects", nil)
	var list listProjectsOut
	if err := json.Unmarshal([]byte(out), &list); isErr || err != nil || len(list.Projects) != 2 {
		t.Fatalf("list_projects: %s", out)
	}
	if p := list.Projects[1]; p.Name != "site" || len(p.Services) != 0 || p.Git != "" || p.Server == "" {
		t.Errorf("empty project: %+v", p)
	}
	if p := list.Projects[0]; p.Name != "shop" || p.Services[0] != "web" || p.Git != "https://github.com/me/shop.git (main) compose.yaml" {
		t.Errorf("shop: %+v", p)
	}

	out, isErr = call(read, "get_project_git", map[string]any{"project": "shop"})
	if isErr || !strings.Contains(out, `"repoUrl":"https://github.com/me/shop.git"`) || strings.Contains(out, "hook-s3cret") {
		t.Errorf("get_project_git: %s", out)
	}
	if out, isErr = call(read, "get_project_git", map[string]any{"project": "site"}); !isErr || !strings.Contains(out, "isn't linked") {
		t.Errorf("get_project_git unlinked: %s", out)
	}

	// Mutations need their scope.
	for name, args := range map[string]map[string]any{
		"create_project":   {"name": "x"},
		"rename_project":   {"project": "site", "name": "x"},
		"rename_service":   {"service": "shop/web", "name": "x"},
		"link_project_git": {"project": "site", "repo_url": "https://github.com/me/site.git"},
		"sync_project_git": {"project": "shop"},
		"service_action":   {"service": "shop/web", "action": "stop"},
	} {
		if out, isErr := call(read, name, args); !isErr || !strings.Contains(out, "needs the") {
			t.Errorf("%s with a read token: %s", name, out)
		}
	}

	if out, isErr = call(admin, "rename_project", map[string]any{"project": "site", "name": "homepage"}); isErr || !strings.Contains(out, `"name":"homepage"`) {
		t.Errorf("rename_project: %s", out)
	}
	if out, isErr = call(admin, "rename_service", map[string]any{"service": "shop/web", "name": "front"}); !isErr || !strings.Contains(out, "git") {
		t.Errorf("rename_service in a git project: %s", out)
	}
	if out, isErr = call(admin, "service_action", map[string]any{"service": "shop/web", "action": "explode"}); !isErr || !strings.Contains(out, "start, stop or restart") {
		t.Errorf("service_action bad action: %s", out)
	}

	if out, isErr = call(read, "get_deployment_log", map[string]any{"service": "shop/web"}); !isErr || !strings.Contains(out, "no deployments") {
		t.Errorf("get_deployment_log without deployments: %s", out)
	}
	dep, err := st.CreateDeployment(ctx, store.Deployment{ServiceID: web.ID, Image: "nginx", Status: store.DeploymentFailed})
	if err != nil {
		t.Fatal(err)
	}
	if out, isErr = call(read, "get_deployment_log", map[string]any{"service": "shop/web"}); isErr || !strings.Contains(out, dep.ID) {
		t.Errorf("get_deployment_log: %s", out)
	}
}
