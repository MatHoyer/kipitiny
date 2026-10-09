package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// TestDeleteTools covers the refusals; actual removal needs Docker.
func TestDeleteTools(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p, _ := e.st.CreateProject(ctx, store.Project{Name: "shop"})
	if _, err := e.st.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1}); err != nil {
		t.Fatal(err)
	}
	read, admin := e.session(store.ScopeRead), e.session(store.ScopeAdmin)

	for name, args := range map[string]map[string]any{
		"delete_service": {"service": "shop/web", "confirm": "web"},
		"delete_project": {"project": "shop", "confirm": "shop"},
	} {
		if out, isErr := e.call(read, name, args); !isErr || !strings.Contains(out, "needs the admin scope") {
			t.Errorf("%s with a read token: %s", name, out)
		}
	}
	if out, isErr := e.call(admin, "delete_service", map[string]any{"service": "shop/web", "confirm": "shop"}); !isErr || !strings.Contains(out, "confirm must repeat the service name") {
		t.Errorf("delete_service unconfirmed: %s", out)
	}
	if out, isErr := e.call(admin, "delete_project", map[string]any{"project": "shop", "confirm": "web"}); !isErr || !strings.Contains(out, "confirm must repeat the project name") {
		t.Errorf("delete_project unconfirmed: %s", out)
	}
	if out, isErr := e.call(admin, "delete_project", map[string]any{"project": "nope", "confirm": "nope"}); !isErr || !strings.Contains(out, "not found") {
		t.Errorf("delete_project unknown: %s", out)
	}

	// A git-linked project's file owns its services.
	if _, err := e.st.SaveProjectGit(ctx, store.ProjectGit{ProjectID: p.ID, RepoURL: "https://github.com/me/shop.git", Branch: "main",
		Path: "compose.yaml", PollSeconds: 300}); err != nil {
		t.Fatal(err)
	}
	if out, isErr := e.call(admin, "delete_service", map[string]any{"service": "shop/web", "confirm": "web"}); !isErr || !strings.Contains(out, "git") {
		t.Errorf("delete_service in a git project: %s", out)
	}
	if _, err := e.st.GetProject(ctx, p.ID); err != nil {
		t.Errorf("project gone: %v", err)
	}
}
