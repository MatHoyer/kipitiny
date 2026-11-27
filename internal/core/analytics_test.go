package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/monitoring"
)

func TestServiceAnalyticsFlag(t *testing.T) {
	app := domain.Service{Name: "web", Kind: domain.ServiceKindApp, Image: "nginx", Replicas: 1, Analytics: true}
	if err := validateService(app); err == nil || !strings.Contains(err.Error(), "set a domain") {
		t.Errorf("without a domain: %v", err)
	}
	app.Domain, app.Port = "shop.example.com", 80
	if err := validateService(app); err != nil {
		t.Errorf("public app: %v", err)
	}

	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	if err := c.validateIn(ctx, app, domain.Project{Name: "shop-pr1", ParentID: "01SHOP", ServerID: domain.LocalServerID}, nil); err == nil ||
		!strings.Contains(err.Error(), "previews") {
		t.Errorf("preview: %v", err)
	}
	if err := c.validateIn(ctx, app, domain.Project{Name: "shop", ServerID: domain.LocalServerID}, nil); err != nil {
		t.Errorf("local project: %v", err)
	}
	remote := domain.Project{Name: "far", ServerID: "01REMOTE"}
	if err := c.validateIn(ctx, app, remote, nil); err == nil || !strings.Contains(err.Error(), "manager's server") {
		t.Errorf("remote server: %v", err)
	}
}

func TestOwnImageRef(t *testing.T) {
	c := newTestCore(t, config.Config{Version: "0.36.0", Update: config.Update{Image: "registry.local/kipitiny"}})
	if got := c.ownImageRef(); got != "registry.local/kipitiny:0.36.0" {
		t.Errorf("release: %s", got)
	}
	c.cfg.Version = "0.36.0-3-gabc-dirty"
	if got := c.ownImageRef(); got != "registry.local/kipitiny:dev" {
		t.Errorf("dev build: %s", got)
	}
	c.cfg.Version = "dev-feat-x"
	if got := c.ownImageRef(); got != "registry.local/kipitiny:dev-feat-x" {
		t.Errorf("test environment: %s", got)
	}
}

// A service sees only the visits of its domain since it took it: a
// hostname released by another project brings none of its history.
func TestAnalyticsSince(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateProject(ctx, domain.Project{Name: "shop", ServerID: domain.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.CreateService(ctx, p.ID, ServiceInput{Name: "web", Image: "nginx", Domain: "shop.example.com", Port: 80, Analytics: true})
	if err != nil {
		t.Fatal(err)
	}
	svc, _ := c.store.GetService(ctx, v.ID)
	if svc.AnalyticsSince.IsZero() {
		t.Fatal("no start recorded")
	}
	start := svc.AnalyticsSince
	time.Sleep(time.Millisecond)
	other := "other.example.com"
	if svc, err = c.updateService(ctx, svc.ID, ServicePatch{Domain: &other}); err != nil || !svc.AnalyticsSince.After(start) {
		t.Fatalf("after a domain change: %v, %v", svc.AnalyticsSince, err)
	}
	stored, _ := c.store.GetService(ctx, svc.ID)
	if !stored.AnalyticsSince.Equal(svc.AnalyticsSince) {
		t.Errorf("stored %v, want %v", stored.AnalyticsSince, svc.AnalyticsSince)
	}
	q := monitoring.AnalyticsQuery{From: start.Add(-time.Hour), To: start.Add(-time.Minute)}.Since(svc)
	if !q.From.Equal(svc.AnalyticsSince) || q.To.Before(q.From) {
		t.Errorf("query = %+v", q)
	}
}
