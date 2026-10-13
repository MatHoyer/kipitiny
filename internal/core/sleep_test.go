package core

import (
	"errors"
	"strings"
	"testing"
	"time"

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
