package core

import (
	"context"
	"errors"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/store"
)

func TestAnalyticsShare(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, _ := c.store.CreateProject(ctx, store.Project{Name: "shop"})
	svc, _ := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1,
		Domain: "shop.example.com", Port: 80})
	if _, err := c.ShareServiceAnalytics(ctx, svc.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("analytics off: %v", err)
	}
	svc.Analytics = true
	c.store.UpdateService(ctx, svc)
	first, err := c.ShareServiceAnalytics(ctx, svc.ID)
	if err != nil || len(first) < 32 {
		t.Fatalf("share: %q %v", first, err)
	}
	// The link resolves to the app: past the token, analytics are off on this manager.
	if _, err := c.SharedAnalytics(ctx, first, AnalyticsQuery{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("shared: %v", err)
	}
	second, _ := c.ShareServiceAnalytics(ctx, svc.ID)
	if _, err := c.SharedAnalytics(ctx, first, AnalyticsQuery{}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("regenerated, old link: %v", err)
	}
	if got, _ := c.store.GetService(ctx, svc.ID); got.AnalyticsShare == "" || got.AnalyticsShare == second || got.AnalyticsSharedAt.IsZero() {
		t.Errorf("stored: %q at %v (the hash, not the token)", got.AnalyticsShare, got.AnalyticsSharedAt)
	}
	// An ordinary update keeps the link.
	got, _ := c.store.GetService(ctx, svc.ID)
	got.Image = "nginx:2"
	c.store.UpdateService(ctx, got)
	if _, err := c.SharedAnalytics(ctx, second, AnalyticsQuery{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("after an update: %v", err)
	}
	c.UnshareServiceAnalytics(ctx, svc.ID)
	if _, err := c.SharedAnalytics(ctx, second, AnalyticsQuery{}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("revoked: %v", err)
	}
	if _, err := c.SharedAnalytics(ctx, "", AnalyticsQuery{}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("empty token: %v", err)
	}
	if got, _ := c.store.GetService(ctx, svc.ID); got.AnalyticsShare != "" || !got.AnalyticsSharedAt.IsZero() {
		t.Errorf("revoked, stored: %+v", got)
	}
}
