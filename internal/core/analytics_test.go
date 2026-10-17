package core

import (
	"context"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestSetAnalyticsValidation(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{Domain: "manager.example.com", Traefik: config.Traefik{Enabled: true}})
	p, _ := c.store.CreateProject(ctx, store.Project{Name: "shop"})
	c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1,
		Domain: "shop.example.com", Port: 80})
	for _, c2 := range []struct {
		in   AnalyticsSettings
		want string
	}{
		{AnalyticsSettings{Enabled: true}, "need a domain"},
		{AnalyticsSettings{Enabled: true, Domain: "not a domain"}, "invalid domain"},
		{AnalyticsSettings{Enabled: true, Domain: "manager.example.com"}, "manager's own"},
		{AnalyticsSettings{Enabled: true, Domain: "shop.example.com"}, "used by web"},
		{AnalyticsSettings{Enabled: true, Domain: "stats.example.com", RetentionDays: 9999}, "1 to 3650 days"},
	} {
		if _, err := c.SetAnalytics(ctx, c2.in); err == nil || !strings.Contains(err.Error(), c2.want) {
			t.Errorf("%+v: %v, want %q", c2.in, err, c2.want)
		}
	}
	if a := c.Analytics(ctx); a.Enabled || a.RetentionDays != defaultAnalyticsRetention {
		t.Errorf("saved after errors: %+v", a)
	}
}

func TestServiceAnalyticsFlag(t *testing.T) {
	app := store.Service{Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1, Analytics: true}
	if err := validateService(app); err == nil || !strings.Contains(err.Error(), "set a domain") {
		t.Errorf("without a domain: %v", err)
	}
	app.Domain, app.Port = "shop.example.com", 80
	if err := validateService(app); err != nil {
		t.Errorf("public app: %v", err)
	}

	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	if err := c.validateIn(ctx, app, store.Project{Name: "shop-pr1", ParentID: "01SHOP", ServerID: store.LocalServerID}, nil); err == nil ||
		!strings.Contains(err.Error(), "previews") {
		t.Errorf("preview: %v", err)
	}
	if err := c.validateIn(ctx, app, store.Project{Name: "shop", ServerID: store.LocalServerID}, nil); err != nil {
		t.Errorf("local project: %v", err)
	}
	remote := store.Project{Name: "far", ServerID: "01REMOTE"}
	if err := c.validateIn(ctx, app, remote, nil); err == nil || !strings.Contains(err.Error(), "manager's server") {
		t.Errorf("remote server: %v", err)
	}
}

func TestAnalyticsImageRef(t *testing.T) {
	c := newTestCore(t, config.Config{Version: "0.36.0"})
	if got := c.analyticsImageRef(); got != "ghcr.io/mathoyer/kipitiny-analytics:0.36.0" {
		t.Errorf("release: %s", got)
	}
	c.cfg.Version = "0.36.0-3-gabc-dirty"
	if got := c.analyticsImageRef(); got != "ghcr.io/mathoyer/kipitiny-analytics:dev" {
		t.Errorf("dev build: %s", got)
	}
	c.cfg.Analytics.Image = "registry.local/a:1"
	if got := c.analyticsImageRef(); got != "registry.local/a:1" {
		t.Errorf("override: %s", got)
	}
}
