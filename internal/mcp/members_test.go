package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// TestMemberTools calls every tool as a member: with view only on a
// project, nothing changes; on a project they weren't granted, nothing is
// read or changed.
func TestMemberTools(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	type seeded struct{ project, service, backup, schedule string }
	seed := func(name string) seeded {
		p, err := e.st.CreateProject(ctx, store.Project{Name: name, ServerID: store.LocalServerID})
		if err != nil {
			t.Fatal(err)
		}
		svc, err := e.st.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "db", Kind: store.ServiceKindPostgres, Image: "postgres:17",
			Replicas: 1, Env: map[string]string{"POSTGRES_PASSWORD": "hunter2"}, Secrets: []string{"POSTGRES_PASSWORD"}})
		if err != nil {
			t.Fatal(err)
		}
		b, err := e.st.CreateBackup(ctx, store.Backup{Kind: store.BackupKindPostgres, ServiceID: svc.ID, ProjectID: p.ID, ServiceName: "db",
			ProjectName: name, ServiceKind: store.ServiceKindPostgres, TargetID: store.LocalTargetID, Status: store.OpSucceeded})
		if err != nil {
			t.Fatal(err)
		}
		sc, err := e.st.CreateBackupSchedule(ctx, store.BackupSchedule{Kind: store.BackupKindPostgres, ServiceID: svc.ID,
			TargetID: store.LocalTargetID, Cron: "@daily", Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		return seeded{project: name, service: name + "/db", backup: b.ID, schedule: sc.ID}
	}
	shop, secret := seed("shop"), seed("secretproj")
	shopID, err := e.c.ProjectOf(ctx, core.IDBackup, shop.backup)
	if err != nil {
		t.Fatal(err)
	}

	member, err := e.st.CreateUser(ctx, store.User{Username: "alice", PasswordHash: "x", Role: store.RoleMember})
	if err != nil {
		t.Fatal(err)
	}
	session := func(perms []core.Perm, features ...core.Feature) *mcp.ClientSession {
		ps := make([]string, len(perms))
		for i, p := range perms {
			ps[i] = string(p)
		}
		if err := e.st.SetGrant(ctx, store.ProjectGrant{UserID: member.ID, ProjectID: shopID, Perms: ps}); err != nil {
			t.Fatal(err)
		}
		fs := make([]string, len(features))
		for i, f := range features {
			fs[i] = string(f)
		}
		u, err := e.st.UpdateUserAccess(ctx, member.ID, store.RoleMember, fs)
		if err != nil {
			t.Fatal(err)
		}
		a, err := e.c.UserActor(ctx, u)
		if err != nil {
			t.Fatal(err)
		}
		return e.sessionOf(core.WithActor(ctx, a))
	}
	// args fills a tool's properties that name a project, service or backup
	// with s's, and its other required ones with harmless values.
	args := func(tool *mcp.Tool, s seeded) map[string]any {
		raw, _ := json.Marshal(tool.InputSchema)
		var schema struct {
			Required   []string       `json:"required"`
			Properties map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		values := map[string]any{
			"project": s.project, "service": s.service, "database": s.service, "backup_id": s.backup, "schedule_id": s.schedule,
			"name": "x", "confirm": "db", "image": "nginx", "action": "restart", "compose": "services: {}\n", "template": e.c.ListTemplates()[0].ID,
			"network": "x", "networks": []string{}, "path": "data/x", "paths": []string{"data/x"}, "from": "data/x", "to": "data/y",
			"query": "select 1", "table": "x", "collection": "x", "key": "x", "provider": "x", "repo": "x/y", "repo_url": "https://example.com/x.git", "after": "0",
		}
		out := map[string]any{}
		for k := range schema.Properties {
			if v, ok := values[k]; ok && (k == "project" || k == "service" || k == "database" || k == "backup_id" || k == "schedule_id") {
				out[k] = v
			}
		}
		for _, k := range schema.Required {
			v, ok := values[k]
			if !ok {
				t.Fatalf("%s: no test value for its required %s", tool.Name, k)
			}
			out[k] = v
		}
		return out
	}

	viewer := session([]core.Perm{core.PermView})
	tools, err := viewer.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		readOnly := tool.Annotations != nil && tool.Annotations.ReadOnlyHint
		if readOnly && tool.Name != "get_audit_log" && tool.Name != "read_volume_file" {
			continue
		}
		out, failed := e.call(viewer, tool.Name, args(tool, shop))
		if !failed || !strings.Contains(out, "forbidden") {
			t.Errorf("%s with view only: %s", tool.Name, out)
		}
	}

	// Errors may repeat the name given; output must not show the project.
	secretSvc, err := e.c.ResolveService(ctx, secret.service)
	if err != nil {
		t.Fatal(err)
	}
	all := session(core.Perms, core.Features...)
	for _, tool := range tools.Tools {
		if tool.Name == "create_project" { // with the feature: a new project of their own
			continue
		}
		out, failed := e.call(all, tool.Name, args(tool, secret))
		leaked := strings.Contains(out, secretSvc.ID) || strings.Contains(out, secretSvc.ProjectID) || strings.Contains(out, "hunter2") ||
			(!failed && strings.Contains(out, "secretproj"))
		if leaked || (!failed && !(tool.Annotations != nil && tool.Annotations.ReadOnlyHint)) {
			t.Errorf("%s on a project not granted: %s", tool.Name, out)
		}
	}
}
