package core

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestValidateRoutes(t *testing.T) {
	ok := store.Service{Kind: store.ServiceKindApp, Domain: "nb.example.com", Port: 80}
	for name, tc := range map[string]struct {
		svc     func(s store.Service) store.Service
		wantErr string
	}{
		"valid": {func(s store.Service) store.Service {
			s.Routes = normalizeRoutes([]store.Route{{Paths: []string{"/a.B/", " /api "}, Scheme: "H2C"}, {Paths: []string{"/relay"}, Scheme: "http", Port: 33080}})
			return s
		}, ""},
		"no domain": {func(s store.Service) store.Service {
			s.Domain, s.Routes = "", []store.Route{{Paths: []string{"/api"}}}
			return s
		}, "need the domain"},
		"no paths": {func(s store.Service) store.Service {
			s.Routes = []store.Route{{}}
			return s
		}, "1 to 20 paths"},
		"relative path": {func(s store.Service) store.Service {
			s.Routes = []store.Route{{Paths: []string{"api"}}}
			return s
		}, "must start with /"},
		"rule injection": {func(s store.Service) store.Service {
			s.Routes = []store.Route{{Paths: []string{"/a`) || Host(`evil"}}}
			return s
		}, "URL path characters"},
		"twice": {func(s store.Service) store.Service {
			s.Routes = []store.Route{{Paths: []string{"/api"}}, {Paths: []string{"/api"}, Scheme: "h2c"}}
			return s
		}, "listed twice"},
		"scheme": {func(s store.Service) store.Service {
			s.Routes = []store.Route{{Paths: []string{"/api"}, Scheme: "https"}}
			return s
		}, "http or h2c"},
		"port": {func(s store.Service) store.Service {
			s.Routes = []store.Route{{Paths: []string{"/api"}, Port: 70000}}
			return s
		}, "out of range"},
	} {
		err := validateRoutes(tc.svc(ok))
		if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.wantErr)
		}
	}
	if got := normalizeRoutes([]store.Route{{Paths: []string{" /x "}, Scheme: "HTTP"}}); got[0].Scheme != "" || got[0].Paths[0] != "/x" {
		t.Errorf("normalize = %+v", got)
	}
}

func TestTraefikLabelsRoutes(t *testing.T) {
	svc := store.Service{ID: "01ABC", Domain: "nb.example.com", Port: 80, Routes: []store.Route{
		{Paths: []string{"/signalexchange.SignalExchange/", "/management.ManagementService/"}, Scheme: "h2c"},
		{Paths: []string{"/relay"}, Port: 8080},
	}}
	l := traefikLabels(svc, route{resolver: "letsencrypt"})
	rules := map[string]string{}
	for k, v := range l {
		if r, ok := strings.CutSuffix(strings.TrimPrefix(k, "traefik.http.routers."), ".rule"); ok && strings.HasPrefix(k, "traefik.http.routers.") {
			rules[v] = r
		}
	}
	if len(rules) != 3 {
		t.Fatalf("routers = %v, want one per path", rules)
	}
	if _, ok := rules["Host(`nb.example.com`)"]; ok {
		t.Error("an app with routes must not take the whole domain")
	}
	at := func(path string) (string, string) {
		r := rules[pathRule("nb.example.com", path)]
		if r == "" {
			t.Fatalf("no router for %s in %v", path, rules)
		}
		if !strings.HasPrefix(r, "kipitiny-01abc-") {
			t.Errorf("router %s: sleep mode reads the service from kipitiny-<id>-", r)
		}
		return r, l["traefik.http.routers."+r+".service"]
	}
	grpc, grpcSvc := at("/signalexchange.SignalExchange/")
	if l["traefik.http.services."+grpcSvc+".loadbalancer.server.scheme"] != "h2c" || l["traefik.http.services."+grpcSvc+".loadbalancer.server.port"] != "80" {
		t.Errorf("h2c service %s labels = %v", grpcSvc, l)
	}
	if strings.Contains(l["traefik.http.routers."+grpc+".middlewares"], "-retry@docker") {
		t.Error("h2c routes must go without retry: it holds bidirectional streams back")
	}
	if l["traefik.http.routers."+grpc+".tls.certresolver"] != "letsencrypt" {
		t.Error("route routers need the certificate")
	}
	relay, relaySvc := at("/relay")
	if relaySvc == grpcSvc || l["traefik.http.services."+relaySvc+".loadbalancer.server.port"] != "8080" ||
		l["traefik.http.services."+relaySvc+".loadbalancer.server.scheme"] != "" {
		t.Errorf("http route service %s labels = %v", relaySvc, l)
	}
	if !strings.HasSuffix(l["traefik.http.routers."+relay+".middlewares"], "kipitiny-01abc-retry@docker") {
		t.Error("http routes keep the retry")
	}
	prio := func(r string) int {
		n, err := strconv.Atoi(l["traefik.http.routers."+r+".priority"])
		if err != nil {
			t.Fatalf("router %s priority: %v", r, err)
		}
		return n
	}
	if prio(relay) != pathPriority("/relay", true) || prio(relay) >= prio(grpc) {
		t.Error("longer paths need a higher priority")
	}

	// Without routes, labels (and so router names) are what they were.
	plain := traefikLabels(store.Service{ID: "01ABC", Domain: "nb.example.com", Port: 80}, route{})
	if plain["traefik.http.routers.kipitiny-01abc-"+specHash("nb.example.com", "", middlewareLabels(store.Middlewares{}, false))[:8]+".rule"] != "Host(`nb.example.com`)" {
		t.Errorf("plain labels = %v", plain)
	}
}

func TestTraefikConfigRoutes(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateProject(ctx, store.Project{Name: "nb", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "server", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1,
		Domain: "nb.example.com", Port: 80, Routes: []store.Route{{Paths: []string{"/grpc.Svc/"}, Scheme: "h2c"}, {Paths: []string{"/api"}}}})
	if err != nil {
		t.Fatal(err)
	}
	token, err := c.providerToken(ctx, store.LocalServerID)
	if err != nil {
		t.Fatal(err)
	}
	get := func() dynamicConfig {
		t.Helper()
		b, err := c.TraefikConfig(ctx, store.LocalServerID, token)
		if err != nil {
			t.Fatal(err)
		}
		var d dynamicConfig
		if err := json.Unmarshal(b, &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	name := "kipitiny-" + strings.ToLower(svc.ID)
	d := get()
	if _, ok := d.HTTP.Routers[name+"-page"]; ok {
		t.Error("an app with routes must not catch the whole domain while down")
	}
	pages := map[string]int{}
	for _, r := range d.HTTP.Routers {
		pages[r.Rule] = r.Priority
	}
	for _, path := range []string{"/grpc.Svc/", "/api"} {
		// Just below the app's own router, above any catch-all.
		if got, ok := pages[pathRule("nb.example.com", path)]; !ok || got != pathPriority(path, false) {
			t.Errorf("page for %s: priority %d, want %d", path, got, pathPriority(path, false))
		}
	}

	// Maintenance without an allowlist: every path shows the page.
	if _, err := c.SetMaintenance(ctx, p.ID, MaintenanceInput{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	d = get()
	n := 0
	for k, r := range d.HTTP.Routers {
		if strings.HasPrefix(k, name+"-maintenance") {
			n++
			if r.Service != pagesService+"@file" || r.Priority <= maintenancePriority {
				t.Errorf("%s = %+v", k, r)
			}
		}
	}
	if n != 2 {
		t.Errorf("maintenance routers = %d, want one per path", n)
	}
}

func TestComposeRoutes(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateProject(ctx, store.Project{Name: "nb", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	src := `
services:
  server:
    image: netbirdio/netbird-server:0.83.2
    expose: ["80"]
    x-kipitiny:
      domain: nb.example.com
      routes:
        - paths: [/signalexchange.SignalExchange/, /management.ManagementService/]
          scheme: h2c
        - paths: [/relay, /api]
          scheme: http
`
	if _, err := c.ApplyCompose(ctx, p.ID, []byte(src), ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	svcs, _ := c.store.ListServices(ctx, p.ID)
	want := []store.Route{{Paths: []string{"/signalexchange.SignalExchange/", "/management.ManagementService/"}, Scheme: "h2c"}, {Paths: []string{"/relay", "/api"}}}
	if !sameRoutes(svcs[0].Routes, want) {
		t.Errorf("routes = %+v", svcs[0].Routes)
	}

	out, err := c.ExportCompose(ctx, p.ID, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := c.ApplyCompose(ctx, p.ID, out.Compose, ApplyOptions{DryRun: true})
	if err != nil || !slices.Equal(plan.Unchanged, []string{"server"}) {
		t.Errorf("own export re-applied: %+v, %v\n%s", plan, err, out.Compose)
	}
	plan, err = c.ApplyCompose(ctx, p.ID, []byte("services: {server: {image: \"netbirdio/netbird-server:0.83.2\", expose: [\"80\"], x-kipitiny: {domain: nb.example.com}}}"), ApplyOptions{DryRun: true})
	if err != nil || len(plan.Update) != 1 || !slices.Equal(plan.Update[0].Fields, []string{"routes"}) {
		t.Errorf("dropping the routes: %+v, %v", plan, err)
	}
	if _, err := c.ApplyCompose(ctx, p.ID, []byte(strings.Replace(src, "scheme: h2c", "scheme: grpc", 1)), ApplyOptions{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad scheme: %v", err)
	}
}
