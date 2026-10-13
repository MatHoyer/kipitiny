package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestParseRouterRequests(t *testing.T) {
	metrics := `# HELP traefik_router_requests_total How many HTTP requests are processed on a router, partitioned by service, status code, protocol, and method.
# TYPE traefik_router_requests_total counter
traefik_router_requests_total{code="200",method="GET",protocol="http",router="kipitiny-01abc-1a2b3c4d@docker",service="kipitiny-01abc@docker"} 12
traefik_router_requests_total{code="404",method="GET",protocol="http",router="kipitiny-01abc-1a2b3c4d@docker",service="kipitiny-01abc@docker"} 3
traefik_router_requests_total{code="200",method="GET",protocol="http",router="kipitiny-01abc-99999999@docker",service="kipitiny-01abc@docker"} 1e+01
traefik_router_requests_total{code="503",method="GET",protocol="http",router="kipitiny-01abc-page@http",service="kipitiny-pages@file"} 7
traefik_router_requests_total{code="200",method="GET",protocol="http",router="kipitiny-manager@file",service="manager@file"} 40
traefik_router_requests_total{code="200",method="GET",protocol="http",router="kipitiny-01def-00000000@docker",service="kipitiny-01def@docker"} 5
traefik_router_request_duration_seconds_bucket{code="200",method="GET",protocol="http",router="kipitiny-01def-00000000@docker",service="kipitiny-01def@docker",le="0.1"} 99
`
	got, err := parseRouterRequests(strings.NewReader(metrics))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["01abc"] != 25 || got["01def"] != 5 {
		t.Fatalf("got %v, want 01abc=25 (both routers, not the page's) and 01def=5", got)
	}
}

func TestSleepObserve(t *testing.T) {
	var s sleepState
	t0 := time.Now()
	if last := s.observe("a", 5, t0); !last.Equal(t0) {
		t.Fatal("a service seen first counts as active")
	}
	if last := s.observe("a", 5, t0.Add(time.Hour)); !last.Equal(t0) {
		t.Fatal("no new request: still idle since the first sight")
	}
	t2 := t0.Add(2 * time.Hour)
	if last := s.observe("a", 6, t2); !last.Equal(t2) {
		t.Fatal("a request is activity")
	}
	t3 := t0.Add(3 * time.Hour)
	if last := s.observe("a", 0, t3); !last.Equal(t3) {
		t.Fatal("a reset counter (Traefik restarted) is activity")
	}
	s.forget(map[string]bool{})
	if len(s.lastActive) != 0 || len(s.requests) != 0 {
		t.Fatal("forget keeps nothing")
	}
}

func TestValidateSleep(t *testing.T) {
	app := store.Service{Kind: store.ServiceKindApp, Domain: "a.example.com", Port: 80}
	for _, tc := range []struct {
		name string
		edit func(*store.Service)
		ok   bool
	}{
		{"off", func(s *store.Service) {}, true},
		{"public app", func(s *store.Service) { s.SleepAfterSeconds = 1800 }, true},
		{"too short", func(s *store.Service) { s.SleepAfterSeconds = 60 }, false},
		{"too long", func(s *store.Service) { s.SleepAfterSeconds = 31 * 86400 }, false},
		{"no domain", func(s *store.Service) { s.SleepAfterSeconds, s.Domain = 1800, "" }, false},
		{"published ports", func(s *store.Service) {
			s.SleepAfterSeconds, s.PublishedPorts = 1800, []store.PublishedPort{{HostPort: 25565, ContainerPort: 25565}}
		}, false},
	} {
		s := app
		tc.edit(&s)
		err := validateSleep(s)
		if (err == nil) != tc.ok || (err != nil && !errors.Is(err, ErrInvalid)) {
			t.Errorf("%s: got %v", tc.name, err)
		}
	}
}

func TestStartingPage(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1,
		Domain: "shop.example.com", Port: 80, SleepAfterSeconds: 1800})
	if err != nil {
		t.Fatal(err)
	}
	if page := c.MaintenancePage(ctx, svc.ID, ""); page.Starting {
		t.Fatal("an awake app gets the usual page")
	}
	if err := c.store.SetServiceSleeping(ctx, svc.ID, true); err != nil {
		t.Fatal(err)
	}
	// Woken just now (the test has no Docker to start containers with).
	c.sleep.waking = map[string]time.Time{svc.ID: time.Now()}
	page := c.MaintenancePage(ctx, "", "https://shop.example.com/cart")
	if body := string(page.Body); !page.Starting || !strings.Contains(body, `http-equiv="refresh"`) || !strings.Contains(body, "shop is starting") {
		t.Fatalf("a sleeping app gets the starting page, got %+v", page)
	}

	// The starting page can be customized like the maintenance page.
	if err := c.store.SetServiceMaintenance(ctx, svc.ID, store.Maintenance{Starting: store.StartingPage{Title: "Waking up", Message: "One moment"}}); err != nil {
		t.Fatal(err)
	}
	if body := string(c.StartingPage(ctx, svc.ID).Body); !strings.Contains(body, "Waking up") || !strings.Contains(body, "One moment") {
		t.Errorf("custom text: %s", body)
	}
	if err := c.store.SetServiceMaintenance(ctx, svc.ID, store.Maintenance{Starting: store.StartingPage{HTML: "<h1>zzz</h1>"}}); err != nil {
		t.Fatal(err)
	}
	if p := c.StartingPage(ctx, svc.ID); !p.Custom || !p.Starting || string(p.Body) != "<h1>zzz</h1>" {
		t.Errorf("custom HTML: %+v", p)
	}
	if err := validateMaintenance(store.Maintenance{Starting: store.StartingPage{HTML: strings.Repeat("a", maxPageHTML+1)}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("huge starting page: %v", err)
	}

	// Woken, not ready yet: still starting; past the cold start, the usual page.
	c.sleep.mu.Lock()
	c.sleep.waking[svc.ID] = time.Now().Add(-coldStart / 2)
	c.sleep.mu.Unlock()
	svc.Sleeping = false
	if !c.starting(svc) {
		t.Fatal("an app woken a minute ago is still starting")
	}
	c.sleep.mu.Lock()
	c.sleep.waking[svc.ID] = time.Now().Add(-coldStart)
	c.sleep.mu.Unlock()
	if c.starting(svc) {
		t.Fatal("past the cold start, the app is down, not starting")
	}

	// A stop or maintenance mode wins over sleep.
	svc.Sleeping, svc.Stopped = true, true
	if c.starting(svc) {
		t.Fatal("a stopped app isn't woken")
	}
	svc.Stopped, svc.Maintenance.Enabled = false, true
	if c.starting(svc) {
		t.Fatal("an app in maintenance mode isn't woken")
	}
}
