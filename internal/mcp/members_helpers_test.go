package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

// seeded names what seed created: a project with a Postgres service, a
// backup of it and a backup schedule.
type seeded struct{ project, service, backup, schedule string }

func (e env) seed(t *testing.T, name string) seeded {
	t.Helper()
	ctx := context.Background()
	p, err := e.st.CreateProject(ctx, domain.Project{Name: name, ServerID: domain.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := e.st.CreateService(ctx, domain.Service{ProjectID: p.ID, Name: "db", Kind: domain.ServiceKindPostgres, Image: "postgres:17",
		Replicas: 1, Env: map[string]string{"POSTGRES_PASSWORD": "hunter2"}, Secrets: []string{"POSTGRES_PASSWORD"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.st.CreateBackup(ctx, domain.Backup{Kind: domain.BackupKindPostgres, ServiceID: svc.ID, ProjectID: p.ID, ServiceName: "db",
		ProjectName: name, ServiceKind: domain.ServiceKindPostgres, TargetID: domain.LocalTargetID, Status: domain.OpSucceeded})
	if err != nil {
		t.Fatal(err)
	}
	sc, err := e.st.CreateBackupSchedule(ctx, domain.BackupSchedule{Kind: domain.BackupKindPostgres, ServiceID: svc.ID,
		TargetID: domain.LocalTargetID, Cron: "@daily", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return seeded{project: name, service: name + "/db", backup: b.ID, schedule: sc.ID}
}

// member creates a member and returns a function that grants them perms on
// the project and features, then connects a session with their token.
func (e env) member(t *testing.T, name, projectID string) func(perms []identity.Perm, features ...identity.Feature) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	m, err := e.st.CreateUser(ctx, domain.User{Username: name, PasswordHash: "x", Role: domain.RoleMember})
	if err != nil {
		t.Fatal(err)
	}
	return func(perms []identity.Perm, features ...identity.Feature) *mcp.ClientSession {
		ps := make([]string, len(perms))
		for i, p := range perms {
			ps[i] = string(p)
		}
		if err := e.st.SetGrant(ctx, domain.ProjectGrant{UserID: m.ID, ProjectID: projectID, Perms: ps}); err != nil {
			t.Fatal(err)
		}
		fs := make([]string, len(features))
		for i, f := range features {
			fs[i] = string(f)
		}
		u, err := e.st.UpdateUserAccess(ctx, m.ID, domain.RoleMember, fs)
		if err != nil {
			t.Fatal(err)
		}
		a, err := e.c.UserActor(ctx, u)
		if err != nil {
			t.Fatal(err)
		}
		return e.sessionOf(identity.WithActor(ctx, a))
	}
}

// toolArgs fills a tool's properties that name a project, service or backup
// with s's, and its other required ones with harmless values.
func (e env) toolArgs(t *testing.T, tool *mcp.Tool, s seeded) map[string]any {
	t.Helper()
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
		"tags": []string{"x"}, "steps": []string{"page:/", "page:/x"}, "visit": "x", "kind": "pixel", "id": "x", "job": "x", "storage": "local", "issue": 1, "status": "resolved",
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
