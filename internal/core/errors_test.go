package core

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestImageRelease(t *testing.T) {
	for in, want := range map[string]string{
		"ghcr.io/org/shop:sha-a1b2c3":            "sha-a1b2c3",
		"registry.local:5000/shop:1.2.0":         "1.2.0",
		"registry.local:5000/shop":               "latest",
		"nginx":                                  "latest",
		"shop@sha256:0123456789abcdef0123":       "0123456789ab",
		"shop:2.0@sha256:0123456789abcdef0123ff": "2.0",
	} {
		if got := ImageRelease(in); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}

func TestServiceErrorsIdentityAndEnv(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{Domain: "manager.example.com", Traefik: config.Traefik{Enabled: true}})
	p, _ := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	// On from the start: a DSN right away.
	v, err := c.CreateService(ctx, p.ID, ServiceInput{Name: "web", Image: "ghcr.io/org/shop:sha-1", Errors: true})
	if err != nil {
		t.Fatal(err)
	}
	web, _ := c.store.GetService(ctx, v.ID)
	if !web.Errors || web.ErrorsProject != 1 || len(web.ErrorsKey) != 32 {
		t.Fatalf("identity: %+v", web)
	}
	// Turned on later: the next number. Off and on again: the same DSN.
	v, _ = c.CreateService(ctx, p.ID, ServiceInput{Name: "worker", Image: "shop-worker"})
	on, off := true, false
	worker, err := c.updateService(ctx, v.ID, ServicePatch{Errors: &on})
	if err != nil || worker.ErrorsProject != 2 {
		t.Fatalf("turned on: %+v %v", worker, err)
	}
	c.updateService(ctx, worker.ID, ServicePatch{Errors: &off})
	again, _ := c.updateService(ctx, worker.ID, ServicePatch{Errors: &on})
	if again.ErrorsProject != 2 || again.ErrorsKey != worker.ErrorsKey {
		t.Errorf("DSN changed: %+v", again)
	}
	// A deleted app's number isn't reused.
	c.store.DeleteService(ctx, web.ID)
	v, _ = c.CreateService(ctx, p.ID, ServiceInput{Name: "api", Image: "shop-api", Errors: true})
	if api, _ := c.store.GetService(ctx, v.ID); api.ErrorsProject != 3 {
		t.Errorf("reused: %d", api.ErrorsProject)
	}
	// Databases have no SDK.
	if _, err := c.CreateService(ctx, p.ID, ServiceInput{Name: "db", Kind: store.ServiceKindPostgres, Errors: true}); err == nil ||
		!strings.Contains(err.Error(), "for apps") {
		t.Errorf("database: %v", err)
	}

	// No variables while analytics are off.
	if env := c.errorsEnv(ctx, p, again); env != nil {
		t.Errorf("analytics off: %v", env)
	}
	b, _ := json.Marshal(analyticsState{AnalyticsSettings: AnalyticsSettings{Enabled: true, Domain: "stats.example.com"}})
	c.store.SetSetting(ctx, analyticsSetting, string(b))
	env := c.errorsEnv(ctx, p, again)
	if env["SENTRY_DSN"] != "https://"+again.ErrorsKey+"@stats.example.com/2" || env["SENTRY_ENVIRONMENT"] != "production" {
		t.Errorf("env: %v", env)
	}
	preview := store.Project{Name: "shop-pr-12", ParentID: p.ID, Preview: store.Preview{Name: "pr-12"}}
	if env := c.errorsEnv(ctx, preview, again); env["SENTRY_ENVIRONMENT"] != "preview-pr-12" {
		t.Errorf("preview: %v", env)
	}

	// The deploy adds them, the app's own variables winning.
	again.Image, again.Env = "shop-worker:v3", map[string]string{"SENTRY_ENVIRONMENT": "staging"}
	spec := containerSpec(p, again, envSources{errors: env}, "dep", 0, route{})
	for _, want := range []string{"SENTRY_DSN=" + env["SENTRY_DSN"], "SENTRY_RELEASE=v3", "SENTRY_ENVIRONMENT=staging"} {
		if !slices.Contains(spec.Config.Env, want) {
			t.Errorf("container env %v lacks %s", spec.Config.Env, want)
		}
	}
	if spec := containerSpec(p, web, envSources{}, "dep", 0, route{}); slices.ContainsFunc(spec.Config.Env, func(e string) bool {
		return strings.HasPrefix(e, "SENTRY_")
	}) {
		t.Errorf("off: %v", spec.Config.Env)
	}
}
