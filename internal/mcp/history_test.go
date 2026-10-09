package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestHistoryTools(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p, _ := e.st.CreateProject(ctx, store.Project{Name: "shop"})
	if _, err := e.st.CreateProject(ctx, store.Project{Name: "site"}); err != nil {
		t.Fatal(err)
	}
	web, err := e.st.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, img := range []string{"nginx:1", "nginx:2", "nginx:3"} {
		if _, err := e.st.CreateDeployment(ctx, store.Deployment{ServiceID: web.ID, Image: img, Status: store.DeploymentSucceeded}); err != nil {
			t.Fatal(err)
		}
	}
	read, deploy, admin := e.session(store.ScopeRead), e.session(store.ScopeDeploy), e.session(store.ScopeAdmin)

	out, isErr := e.call(read, "list_deployments", map[string]any{"service": "shop/web", "limit": 2})
	var deps listDeploymentsOut
	if err := json.Unmarshal([]byte(out), &deps); isErr || err != nil || len(deps.Deployments) != 2 || deps.Deployments[0].Image != "nginx:3" {
		t.Errorf("list_deployments: %s", out)
	}
	if out, isErr = e.call(read, "list_restores", map[string]any{"service": "shop/web"}); isErr || out != `{"restores":[]}` {
		t.Errorf("list_restores: %s", out)
	}

	for name, args := range map[string]map[string]any{
		"backup_project": {"project": "shop"},
		"verify_backup":  {"backup_id": "x"},
		"delete_backup":  {"backup_id": "x"},
	} {
		if out, isErr := e.call(read, name, args); !isErr || !strings.Contains(out, "needs the") {
			t.Errorf("%s with a read token: %s", name, out)
		}
	}
	if out, isErr = e.call(deploy, "delete_backup", map[string]any{"backup_id": "x"}); !isErr || !strings.Contains(out, "needs the admin scope") {
		t.Errorf("delete_backup with a deploy token: %s", out)
	}
	if out, isErr = e.call(deploy, "backup_project", map[string]any{"project": "site"}); !isErr || !strings.Contains(out, "no databases or volumes") {
		t.Errorf("backup_project without data: %s", out)
	}
	if out, isErr = e.call(deploy, "verify_backup", map[string]any{"backup_id": "nope"}); !isErr || !strings.Contains(out, "not found") {
		t.Errorf("verify_backup unknown: %s", out)
	}
	if out, isErr = e.call(admin, "delete_backup", map[string]any{"backup_id": "nope"}); !isErr || !strings.Contains(out, "not found") {
		t.Errorf("delete_backup unknown: %s", out)
	}
}
