package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/core"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestPreviewSettingsTools(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p, _ := e.st.CreateProject(ctx, store.Project{Name: "shop"})
	read, admin := e.session(core.PermView), e.session()

	if out, isErr := e.call(read, "set_preview_settings", map[string]any{"project": "shop", "ttl": "72h"}); !isErr || !strings.Contains(out, "forbidden") {
		t.Errorf("set with a read token: %s", out)
	}
	if out, isErr := e.call(admin, "set_preview_settings", map[string]any{"project": "shop", "base_domain": "pr.example.com",
		"set": map[string]any{"MODE": "preview"}, "secrets": map[string]any{"API_KEY": "s3cret"}, "ttl": "72h"}); isErr || strings.Contains(out, "s3cret") {
		t.Fatalf("set_preview_settings: %s", out)
	}
	// An update keeps what it doesn't set, secrets included.
	if out, isErr := e.call(admin, "set_preview_settings", map[string]any{"project": "shop", "sleep_after": "30m", "unset": []string{"MODE"}}); isErr {
		t.Fatalf("update: %s", out)
	}
	ps, err := e.st.GetPreviewSettings(ctx, p.ID)
	if err != nil || ps.BaseDomain != "pr.example.com" || ps.Env["API_KEY"] != "s3cret" || ps.Env["MODE"] != "" || ps.TTLSeconds != 72*3600 || ps.SleepAfterSeconds != 1800 {
		t.Errorf("stored: %+v %v", ps, err)
	}
	if out, isErr := e.call(read, "get_preview_settings", map[string]any{"project": "shop"}); isErr || strings.Contains(out, "s3cret") || !strings.Contains(out, "API_KEY") {
		t.Errorf("get_preview_settings: %s", out)
	}
	if out, isErr := e.call(admin, "set_preview_settings", map[string]any{"project": "shop", "ttl": "soon"}); !isErr {
		t.Errorf("bad ttl: %s", out)
	}
}

func TestAnonymizeRulesTools(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p, _ := e.st.CreateProject(ctx, store.Project{Name: "shop"})
	db, _ := e.st.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "db", Kind: store.ServiceKindPostgres, Image: "postgres:17"})
	read, admin := e.session(core.PermView), e.session()

	rules := map[string]any{"users": map[string]any{"email": "fake:email"}, "sessions": "truncate"}
	if out, isErr := e.call(read, "set_anonymize_rules", map[string]any{"project": "shop", "service": "db", "rules": rules}); !isErr || !strings.Contains(out, "forbidden") {
		t.Errorf("set with a read token: %s", out)
	}
	if out, isErr := e.call(admin, "set_anonymize_rules", map[string]any{"project": "shop", "service": "db", "rules": rules}); isErr {
		t.Fatalf("set_anonymize_rules: %s", out)
	}
	if got, _ := e.st.GetService(ctx, db.ID); got.Anonymize["sessions"].Action != "truncate" || got.Anonymize["users"].Columns["email"] != "fake:email" {
		t.Errorf("stored: %+v", got.Anonymize)
	}
	if out, isErr := e.call(read, "get_preview_seed", map[string]any{"project": "shop"}); isErr || !strings.Contains(out, `"truncate"`) {
		t.Errorf("get_preview_seed: %s", out)
	}
	if out, isErr := e.call(admin, "set_anonymize_rules", map[string]any{"project": "shop", "service": "db", "rules": map[string]any{"users": "drop"}}); !isErr {
		t.Errorf("bad rule: %s", out)
	}
	if out, isErr := e.call(admin, "set_anonymize_rules", map[string]any{"project": "shop", "service": "db", "remove": true}); isErr {
		t.Fatalf("remove: %s", out)
	}
	if got, _ := e.st.GetService(ctx, db.ID); got.Anonymize != nil {
		t.Errorf("not removed: %+v", got.Anonymize)
	}
}
