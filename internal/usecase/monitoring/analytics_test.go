package monitoring

import (
	"context"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestSetAnalyticsValidation(t *testing.T) {
	ctx := context.Background()
	c := newTestMonitoring(t, config.Config{Domain: "manager.example.com", Traefik: config.Traefik{Enabled: true}})
	p, _ := c.store.CreateProject(ctx, domain.Project{Name: "shop"})
	if _, err := c.store.CreateService(ctx, domain.Service{ProjectID: p.ID, Name: "web", Kind: domain.ServiceKindApp, Image: "nginx", Replicas: 1,
		Domain: "shop.example.com", Port: 80}); err != nil {
		t.Fatal(err)
	}
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
