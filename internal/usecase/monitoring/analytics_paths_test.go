package monitoring

import (
	"context"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestAnalyticsPaths(t *testing.T) {
	for _, c := range []struct {
		script, hit string
		ok          bool
	}{
		{"", "", true},
		{"/js/app.js", "/api/collect", true},
		{"/js/app.js", "", false},
		{"/same", "/same", false},
		{"/", "/x", false},
		{"/x`) || Host(`evil", "/y", false},
		{"js/app.js", "/y", false},
	} {
		if err := ValidateAnalyticsPaths(c.script, c.hit); (err == nil) != c.ok {
			t.Errorf("%q %q: %v", c.script, c.hit, err)
		}
	}

	ctx := context.Background()
	co := newTestMonitoring(t, config.Config{})
	own := domain.Service{ID: "01OWN", Domain: "shop.example.com", ServerID: domain.LocalServerID, AnalyticsScript: "/js/k.js", AnalyticsHit: "/k"}
	plain := domain.Service{ID: "01PLAIN", Domain: "blog.example.com", ServerID: domain.LocalServerID}
	labels := co.analyticsSpec(ctx, AnalyticsState{AnalyticsSettings: AnalyticsSettings{Domain: "stats.example.com"}}, []domain.Service{own, plain}).Labels
	if r := labels["traefik.http.routers.kipitiny-analytics-01own.rule"]; r != "Host(`shop.example.com`) && (Path(`/js/k.js`) || Path(`/k`))" {
		t.Errorf("own paths' router: %q", r)
	}
	if labels["traefik.http.routers.kipitiny-analytics-01own.service"] != analyticsRouter || labels["traefik.http.routers.kipitiny-analytics-01plain.rule"] != "" {
		t.Errorf("labels: %v", labels)
	}
	if s := serviceScript(own, "stats.example.com"); s != `<script defer src="https://shop.example.com/js/k.js"></script>` {
		t.Errorf("own script tag: %s", s)
	}
	if s := serviceScript(plain, "stats.example.com"); !strings.Contains(s, "https://stats.example.com/a.js") {
		t.Errorf("default script tag: %s", s)
	}
}
