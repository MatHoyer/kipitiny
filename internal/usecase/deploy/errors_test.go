package deploy

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/monitoring"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

func TestServiceErrorsIdentityAndEnv(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{Domain: "manager.example.com", Traefik: config.Traefik{Enabled: true}})
	p, _ := c.store.CreateProject(ctx, domain.Project{Name: "shop", ServerID: domain.LocalServerID})
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
	if _, err := c.updateService(ctx, worker.ID, ServicePatch{Errors: &off}); err != nil {
		t.Fatal(err)
	}
	again, _ := c.updateService(ctx, worker.ID, ServicePatch{Errors: &on})
	if again.ErrorsProject != 2 || again.ErrorsKey != worker.ErrorsKey {
		t.Errorf("DSN changed: %+v", again)
	}
	// A deleted app's number isn't reused.
	if err := c.store.DeleteService(ctx, web.ID); err != nil {
		t.Fatal(err)
	}
	v, _ = c.CreateService(ctx, p.ID, ServiceInput{Name: "api", Image: "shop-api", Errors: true})
	if api, _ := c.store.GetService(ctx, v.ID); api.ErrorsProject != 3 {
		t.Errorf("reused: %d", api.ErrorsProject)
	}
	// Databases have no SDK.
	if _, err := c.CreateService(ctx, p.ID, ServiceInput{Name: "db", Kind: domain.ServiceKindPostgres, Errors: true}); err == nil ||
		!strings.Contains(err.Error(), "for apps") {
		t.Errorf("database: %v", err)
	}

	// No variables while analytics are off.
	if env := c.ErrorsEnv(ctx, p, again); env != nil {
		t.Errorf("analytics off: %v", env)
	}
	b, _ := json.Marshal(monitoring.AnalyticsState{AnalyticsSettings: monitoring.AnalyticsSettings{Enabled: true, Domain: "stats.example.com"}})
	if err := c.store.SetSetting(ctx, monitoring.AnalyticsSetting, string(b)); err != nil {
		t.Fatal(err)
	}
	env := c.ErrorsEnv(ctx, p, again)
	if env["SENTRY_DSN"] != "https://"+again.ErrorsKey+"@stats.example.com/2" || env["SENTRY_ENVIRONMENT"] != "production" {
		t.Errorf("env: %v", env)
	}
	preview := domain.Project{Name: "shop-pr-12", ParentID: p.ID, Preview: domain.Preview{Name: "pr-12"}}
	if env := c.ErrorsEnv(ctx, preview, again); env["SENTRY_ENVIRONMENT"] != "preview-pr-12" {
		t.Errorf("preview: %v", env)
	}

	// The deploy adds them, the app's own variables winning.
	again.Image, again.Env = "shop-worker:v3", map[string]string{"SENTRY_ENVIRONMENT": "staging"}
	spec := containerSpec(p, again, envSources{errors: env}, "dep", 0, network.Routing{})
	for _, want := range []string{"SENTRY_DSN=" + env["SENTRY_DSN"], "SENTRY_RELEASE=v3", "SENTRY_ENVIRONMENT=staging"} {
		if !slices.Contains(spec.Env, want) {
			t.Errorf("container env %v lacks %s", spec.Env, want)
		}
	}
	if spec := containerSpec(p, web, envSources{}, "dep", 0, network.Routing{}); slices.ContainsFunc(spec.Env, func(e string) bool {
		return strings.HasPrefix(e, "SENTRY_")
	}) {
		t.Errorf("off: %v", spec.Env)
	}
}
