package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
)

// TestMemberTools calls every tool as a member: with view only on a
// project, nothing changes; on a project they weren't granted, nothing is
// read or changed.
func TestMemberTools(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	shop, secret := e.seed(t, "shop"), e.seed(t, "secretproj")
	shopID, err := e.c.ProjectOf(ctx, identity.IDBackup, shop.backup)
	if err != nil {
		t.Fatal(err)
	}

	session := e.member(t, "alice", shopID)

	viewer := session([]identity.Perm{identity.PermView})
	tools, err := viewer.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		readOnly := tool.Annotations != nil && tool.Annotations.ReadOnlyHint
		if readOnly && tool.Name != "get_audit_log" && tool.Name != "read_volume_file" {
			continue
		}
		out, failed := e.call(viewer, tool.Name, e.toolArgs(t, tool, shop))
		if !failed || !strings.Contains(out, "forbidden") {
			t.Errorf("%s with view only: %s", tool.Name, out)
		}
	}

	// Errors may repeat the name given; output must not show the project.
	secretSvc, err := e.c.ResolveService(ctx, secret.service)
	if err != nil {
		t.Fatal(err)
	}
	all := session(identity.Perms, identity.Features...)
	for _, tool := range tools.Tools {
		if tool.Name == "create_project" { // with the feature: a new project of their own
			continue
		}
		out, failed := e.call(all, tool.Name, e.toolArgs(t, tool, secret))
		leaked := strings.Contains(out, secretSvc.ID) || strings.Contains(out, secretSvc.ProjectID) || strings.Contains(out, "hunter2") ||
			(!failed && strings.Contains(out, "secretproj"))
		if leaked || (!failed && (tool.Annotations == nil || !tool.Annotations.ReadOnlyHint)) {
			t.Errorf("%s on a project not granted: %s", tool.Name, out)
		}
	}
}
