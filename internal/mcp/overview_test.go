package mcp

import (
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestOverviewTools(t *testing.T) {
	e := newEnv(t)
	read, admin := e.session(store.ScopeRead), e.session(store.ScopeAdmin)

	if out, isErr := e.call(read, "list_git_repos", nil); isErr || out != `{"providers":[]}` {
		t.Errorf("list_git_repos without providers: %s", out)
	}
	if out, isErr := e.call(read, "list_git_repos", map[string]any{"provider": "github"}); !isErr || !strings.Contains(out, "not found") {
		t.Errorf("list_git_repos unknown provider: %s", out)
	}
	if out, isErr := e.call(read, "list_git_branches", map[string]any{"provider": "github", "repo": "me/shop"}); !isErr || !strings.Contains(out, "not found") {
		t.Errorf("list_git_branches unknown provider: %s", out)
	}
	if out, isErr := e.call(read, "get_topology", map[string]any{"project": "nope"}); !isErr || !strings.Contains(out, "not found") {
		t.Errorf("get_topology unknown project: %s", out)
	}
	if out, isErr := e.call(read, "get_manager_status", nil); isErr || !strings.Contains(out, `"usage":{}`) || !strings.Contains(out, `"update"`) {
		t.Errorf("get_manager_status: %s", out)
	}

	// The audit log is admin only, and holds refused attempts.
	if out, isErr := e.call(read, "get_audit_log", nil); !isErr || !strings.Contains(out, "needs the admin scope") {
		t.Errorf("get_audit_log with a read token: %s", out)
	}
	e.call(read, "delete_project", map[string]any{"project": "shop", "confirm": "shop"})
	if out, isErr := e.call(admin, "get_audit_log", map[string]any{"limit": 5}); isErr || !strings.Contains(out, `"action":"mcp delete_project"`) || !strings.Contains(out, `"status":403`) {
		t.Errorf("get_audit_log: %s", out)
	}
}
