package core

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/compose"
	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// previewParent is a project like most: an app on a domain using a
// database, a worker, a game server and a secret.
func previewParent(t *testing.T, c *Core) store.Project {
	t.Helper()
	ctx := context.Background()
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID,
		Env:     map[string]string{"LOG_LEVEL": "info", "STRIPE_KEY": "sk_live", "SENTRY_DSN": "https://sentry", "MAIL": "{{ pass://Prod/mail/password }}"},
		Secrets: []string{"SENTRY_DSN", "STRIPE_KEY"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []store.Service{
		{Name: "db", Kind: store.ServiceKindPostgres, Image: "postgres:17", Env: map[string]string{pgUser: "u", pgPassword: "prod-pass", pgDatabase: "app"}},
		{Name: "web", Kind: store.ServiceKindApp, Image: "ghcr.io/acme/web:v1", Domain: "shop.example.com", Port: 3000, Replicas: 3,
			Env:     map[string]string{"DATABASE_URL": "{{ db.db.URL }}", "APP_URL": "{{ app.web.URL }}", "API_TOKEN": "t0ken", "STRIPE": "{{ project.STRIPE_KEY }}"},
			Secrets: []string{"API_TOKEN"}},
		{Name: "worker", Kind: store.ServiceKindApp, Image: "ghcr.io/acme/web:v1", Env: map[string]string{"LOG": "{{ project.LOG_LEVEL }}"}},
		{Name: "game", Kind: store.ServiceKindApp, Image: "itzg/minecraft-server", PublishedPorts: []store.PublishedPort{{HostPort: 25565, ContainerPort: 25565, Protocol: "tcp"}}},
	} {
		s.ProjectID, s.ServerID = p.ID, p.ServerID
		if s.Replicas == 0 {
			s.Replicas = 1
		}
		if _, err := c.store.CreateService(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func TestPreviewFile(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	parent := previewParent(t, c)
	f, vars, _, err := c.previewSource(ctx, parent, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(vars) != 0 {
		t.Errorf("no basic auth: no variable kept, got %v", vars)
	}
	settings := store.PreviewSettings{BaseDomain: "preview.example.com"}
	warnings, err := previewFile(&f, parent, "pr-12", settings, map[string]string{"web": "sha-abc", "worker": "sha256:" + strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	web, worker, game, db := f.Services["web"], f.Services["worker"], f.Services["game"], f.Services["db"]
	switch {
	case f.Name != "shop-pr-12" || len(f.X.Variables) != 0:
		t.Errorf("file: %s %v", f.Name, f.X.Variables)
	case web.X.Domain != "pr-12.preview.example.com" || web.Replicas != 0 || web.Image != "ghcr.io/acme/web:sha-abc":
		t.Errorf("web: %s ×%d %s", web.X.Domain, web.Replicas, web.Image)
	case int(web.X.SleepAfter) != int(defaultPreviewSleep.Seconds()) || web.X.Middlewares.Headers["X-Robots-Tag"] != "noindex":
		t.Errorf("web sleeps %d, middlewares %+v", web.X.SleepAfter, web.X.Middlewares)
	case web.Environment["API_TOKEN"] != "{{ project.API_TOKEN }}" || web.Environment["APP_URL"] != "{{ app.web.URL }}":
		t.Errorf("web env: %v", web.Environment)
	case !strings.HasSuffix(worker.Image, "@sha256:"+strings.Repeat("a", 64)) || worker.X.SleepAfter != 0:
		t.Errorf("worker: %s sleeps %d", worker.Image, worker.X.SleepAfter)
	case len(game.Ports) != 0 || len(warnings) != 1:
		t.Errorf("game ports %v, warnings %v", game.Ports, warnings)
	case db.X.Password != "":
		t.Errorf("db gets a fresh password, has %q", db.X.Password)
	}
	if _, err := compose.Marshal(f); err != nil {
		t.Fatal(err)
	}

	if _, err := previewFile(&f, parent, "pr-12", settings, map[string]string{"nope": "v2"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a tag for a missing service: %v", err)
	}
}

func TestPreviewEnv(t *testing.T) {
	c := newTestCore(t, config.Config{})
	parent := previewParent(t, c)
	settings := store.PreviewSettings{
		Env:     map[string]string{"STRIPE_KEY": "sk_test", "API_TOKEN": "preview-token", "LOG_LEVEL": "debug"},
		Secrets: []string{"API_TOKEN", "STRIPE_KEY"},
		Inherit: []string{"SENTRY_DSN"},
	}
	env, secrets := previewEnv(parent, settings, map[string]string{"FEATURE": "on"})
	want := map[string]string{"STRIPE_KEY": "sk_test", "API_TOKEN": "preview-token", "LOG_LEVEL": "debug", "SENTRY_DSN": "https://sentry", "FEATURE": "on"}
	if !maps.Equal(env, want) {
		t.Errorf("env = %v, want %v (no MAIL: it points at production's password manager)", env, want)
	}
	if !slices.Equal(secrets, []string{"API_TOKEN", "SENTRY_DSN", "STRIPE_KEY"}) {
		t.Errorf("secrets = %v", secrets)
	}
}

func TestCheckPreviewEnv(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	parent := previewParent(t, c)
	parentSvcs, _ := c.store.ListServices(ctx, parent.ID)
	f, _, _, err := c.previewSource(ctx, parent, "")
	if err != nil {
		t.Fatal(err)
	}
	settings := store.PreviewSettings{BaseDomain: "preview.example.com"}
	if _, err := previewFile(&f, parent, "pr-1", settings, nil); err != nil {
		t.Fatal(err)
	}

	env, _ := previewEnv(parent, settings, nil)
	err = checkPreviewEnv(f, env, settings, parentSvcs)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "API_TOKEN, STRIPE_KEY") {
		t.Fatalf("missing secrets are refused, never production's: %v", err)
	}

	settings.Env = map[string]string{"API_TOKEN": "x", "STRIPE_KEY": "sk_test"}
	env, _ = previewEnv(parent, settings, nil)
	if err := checkPreviewEnv(f, env, settings, parentSvcs); err != nil {
		t.Fatalf("all set: %v", err)
	}

	web := f.Services["web"]
	web.Environment["CALLBACK"] = "https://shop.example.com/auth"
	if err := checkPreviewEnv(f, env, settings, parentSvcs); err == nil || !strings.Contains(err.Error(), "web's CALLBACK (shop.example.com)") {
		t.Errorf("a production URL is refused: %v", err)
	}
	web.Environment["CALLBACK"] = "{{ project.PUBLIC }}"
	settings.Env["PUBLIC"] = "https://shop.example.com"
	env, _ = previewEnv(parent, settings, nil)
	if err := checkPreviewEnv(f, env, settings, parentSvcs); err != nil {
		t.Errorf("set in the preview settings on purpose: %v", err)
	}
}

func TestPreviewSettings(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	parent := previewParent(t, c)
	ps, err := c.SetPreviewSettings(ctx, parent.ID, PreviewSettingsInput{BaseDomain: "Preview.Example.com",
		Env: map[string]string{"STRIPE_KEY": "sk_test", "LOG_LEVEL": "debug"}, Secrets: []string{"STRIPE_KEY"}, Inherit: []string{"SENTRY_DSN"}})
	if err != nil {
		t.Fatal(err)
	}
	if ps.BaseDomain != "preview.example.com" || ps.Env["STRIPE_KEY"] != SecretMask || ps.Env["LOG_LEVEL"] != "debug" {
		t.Errorf("saved %+v", ps)
	}
	// The mask sent back keeps the secret.
	if _, err := c.SetPreviewSettings(ctx, parent.ID, PreviewSettingsInput{BaseDomain: "preview.example.com",
		Env: map[string]string{"STRIPE_KEY": SecretMask}, Secrets: []string{"STRIPE_KEY"}}); err != nil {
		t.Fatal(err)
	}
	if stored, _ := c.store.GetPreviewSettings(ctx, parent.ID); stored.Env["STRIPE_KEY"] != "sk_test" || len(stored.Inherit) != 0 {
		t.Errorf("stored %+v", stored)
	}
	for name, in := range map[string]PreviewSettingsInput{
		"bad domain":   {BaseDomain: "not a domain"},
		"not a secret": {Inherit: []string{"LOG_LEVEL"}},
		"short sleep":  {SleepAfterSeconds: 60},
		"negative ttl": {TTLSeconds: -1},
		"bad env name": {Env: map[string]string{"1X": "y"}},
	} {
		if _, err := c.SetPreviewSettings(ctx, parent.ID, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := c.DeployPreview(ctx, parent.ID, "Bad_Name", PreviewOptions{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad preview name: %v", err)
	}
}
