package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestVolumeFileTools(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p, _ := e.st.CreateProject(ctx, store.Project{Name: "game"})
	for _, svc := range []store.Service{
		{ProjectID: p.ID, Name: "mc", Kind: store.ServiceKindApp, Image: "itzg/minecraft-server", Replicas: 1, Volumes: []store.Volume{{Name: "data", Path: "/data"}}},
		{ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1},
		{ProjectID: p.ID, Name: "db", Kind: store.ServiceKindPostgres, Image: "postgres:17", Replicas: 1, Env: map[string]string{}},
	} {
		if _, err := e.st.CreateService(ctx, svc); err != nil {
			t.Fatal(err)
		}
	}
	read, admin := e.session(store.ScopeRead), e.session(store.ScopeAdmin)

	// The volumes themselves come from the settings, without Docker.
	out, isErr := e.call(read, "list_volume_files", map[string]any{"service": "game/mc"})
	if isErr || !strings.Contains(out, `"name":"data"`) || !strings.Contains(out, `"type":"volume"`) || strings.Contains(out, `"modified"`) || !strings.Contains(out, `"mountPath":"/data"`) || !strings.Contains(out, `"readOnly":false`) {
		t.Errorf("list_volume_files: %s", out)
	}
	if out, isErr = e.call(read, "list_volume_files", map[string]any{"service": "game/db"}); isErr || !strings.Contains(out, `"readOnly":true`) {
		t.Errorf("list_volume_files of a database: %s", out)
	}

	for name, args := range map[string]map[string]any{
		"write_volume_file":   {"service": "game/mc", "path": "data/x", "content": "x"},
		"make_volume_dir":     {"service": "game/mc", "path": "data/x"},
		"move_volume_path":    {"service": "game/mc", "from": "data/x", "to": "data/y"},
		"delete_volume_paths": {"service": "game/mc", "paths": []string{"data/x"}},
	} {
		if out, isErr := e.call(read, name, args); !isErr || !strings.Contains(out, "needs the admin scope") {
			t.Errorf("%s with a read token: %s", name, out)
		}
	}

	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"write_volume_file", map[string]any{"service": "game/db", "path": "data/x", "content": "x"}, "read-only"},
		{"delete_volume_paths", map[string]any{"service": "game/db", "paths": []string{"data/PG_VERSION"}}, "read-only"},
		{"write_volume_file", map[string]any{"service": "game/mc", "path": "data/x", "content": "x", "content_base64": "eA=="}, "not both"},
		{"write_volume_file", map[string]any{"service": "game/mc", "path": "data/x", "content_base64": "%%%"}, "content_base64"},
		{"write_volume_file", map[string]any{"service": "game/mc", "path": "data/x", "content": "x", "modified": "yesterday"}, "RFC 3339"},
		{"write_volume_file", map[string]any{"service": "game/mc", "path": "data", "content": "x"}, "inside a volume"},
		{"make_volume_dir", map[string]any{"service": "game/mc", "path": "data/../../etc"}, "must not contain .."},
		{"move_volume_path", map[string]any{"service": "game/mc", "from": "data/a", "to": "data/a/b"}, "into itself"},
		{"delete_volume_paths", map[string]any{"service": "game/mc", "paths": []string{}}, "nothing to delete"},
		{"make_volume_dir", map[string]any{"service": "game/mc", "path": "logs/x"}, "not found"},
		{"make_volume_dir", map[string]any{"service": "game/web", "path": "data/x"}, "not found"},
	} {
		if out, isErr := e.call(admin, tc.name, tc.args); !isErr || !strings.Contains(out, tc.want) {
			t.Errorf("%s %v: %s, want %q", tc.name, tc.args, out, tc.want)
		}
	}
	if out, isErr = e.call(read, "read_volume_file", map[string]any{"service": "game/mc", "path": "data/a"}); !isErr || !strings.Contains(out, "needs the admin scope") {
		t.Errorf("read_volume_file with a read token: %s", out)
	}
	if out, isErr = e.call(admin, "read_volume_file", map[string]any{"service": "game/web", "path": ""}); !isErr || !strings.Contains(out, "not a file") {
		t.Errorf("read_volume_file of the root: %s", out)
	}
}
