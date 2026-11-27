package core

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

func TestSetMaintenanceValidation(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateProject(ctx, domain.Project{Name: "shop", ServerID: domain.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.CreateService(ctx, domain.Service{ProjectID: p.ID, Name: "db", Kind: domain.ServiceKindPostgres, Image: "postgres", Replicas: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.CreateService(ctx, domain.Service{ProjectID: p.ID, Name: "worker", Kind: domain.ServiceKindApp, Image: "busybox", Replicas: 1}); err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string]MaintenanceInput{
		"no app on a domain": {Enabled: true},
		"bad range":          {AllowIPs: []string{"10.0.0.0/33"}},
		"long title":         {Title: strings.Repeat("a", maxPageTitle+1)},
		"huge page":          {HTML: strings.Repeat("a", maxPageHTML+1)},
		"not utf-8 html":     {HTML: "\xff"},
	} {
		if _, err := c.SetMaintenance(ctx, p.ID, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if got, err := c.SetMaintenance(ctx, p.ID, MaintenanceInput{Title: " Back soon "}); err != nil || got.Maintenance.Title != "Back soon" {
		t.Errorf("page without an app on a domain: %+v, %v", got.Maintenance, err)
	}
}

func TestMaintenancePage(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateProject(ctx, domain.Project{Name: "shop", ServerID: domain.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := c.store.CreateService(ctx, domain.Service{ProjectID: p.ID, Name: "web", Kind: domain.ServiceKindApp, Image: "nginx", Replicas: 1, Domain: "shop.example.com", Port: 80})
	if err != nil {
		t.Fatal(err)
	}

	page := c.MaintenancePage(ctx, "", "https://Shop.example.com/cart?x=1")
	if body := string(page.Body); page.Custom || !strings.Contains(body, "We&#39;ll be back soon") || !strings.Contains(body, "shop is unavailable right now") {
		t.Errorf("default page by URL:\n%s", body)
	}
	if body := string(c.MaintenancePage(ctx, "", "https://other.example.com/").Body); !strings.Contains(body, "This site is unavailable") {
		t.Errorf("unknown host:\n%s", body)
	}

	m := domain.Maintenance{Enabled: true, Title: "Upgrading <db>", Message: "Back at 3pm."}
	if err := c.store.SetProjectMaintenance(ctx, p.ID, m); err != nil {
		t.Fatal(err)
	}
	body := string(c.MaintenancePage(ctx, svc.ID, "").Body)
	if !strings.Contains(body, "<h1>Upgrading &lt;db&gt;</h1>") || !strings.Contains(body, "Back at 3pm.") {
		t.Errorf("custom text:\n%s", body)
	}
	m = domain.Maintenance{Enabled: true}
	if err := c.store.SetProjectMaintenance(ctx, p.ID, m); err != nil {
		t.Fatal(err)
	}
	if body := string(c.MaintenancePage(ctx, svc.ID, "").Body); !strings.Contains(body, "shop is down for maintenance") {
		t.Errorf("maintenance mode text:\n%s", body)
	}

	m.HTML = "<h1>Gone fishing</h1>"
	if err := c.store.SetProjectMaintenance(ctx, p.ID, m); err != nil {
		t.Fatal(err)
	}
	if page := c.MaintenancePage(ctx, svc.ID, ""); !page.Custom || string(page.Body) != m.HTML {
		t.Errorf("custom HTML: %+v", page)
	}
	// The project's own ID serves its page too (to see it from the settings).
	if page := c.MaintenancePage(ctx, p.ID, ""); !page.Custom || string(page.Body) != m.HTML {
		t.Errorf("page by project: %+v", page)
	}
	// Every app of the project shows it.
	if _, err := c.store.CreateService(ctx, domain.Service{ProjectID: p.ID, Name: "api", Kind: domain.ServiceKindApp, Image: "nginx", Replicas: 1, Domain: "api.shop.example.com", Port: 80}); err != nil {
		t.Fatal(err)
	}
	if page := c.MaintenancePage(ctx, "", "https://api.shop.example.com/"); string(page.Body) != m.HTML {
		t.Errorf("another app of the project: %s", page.Body)
	}
	// A rename keeps it.
	if err := c.store.RenameProject(ctx, p.ID, "store"); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.store.GetProject(ctx, p.ID); got.Maintenance.HTML != m.HTML || !got.Maintenance.Enabled {
		t.Errorf("maintenance after rename = %+v", got.Maintenance)
	}
}

type dynamicConfig struct {
	HTTP struct {
		Routers map[string]struct {
			Rule        string
			Priority    int
			Service     string
			Middlewares []string
			TLS         map[string]string `json:"tls"`
		}
		Middlewares map[string]map[string]json.RawMessage
	}
}

func TestTraefikConfig(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateProject(ctx, domain.Project{Name: "shop", ServerID: domain.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	web, err := c.store.CreateService(ctx, domain.Service{ProjectID: p.ID, Name: "web", Kind: domain.ServiceKindApp, Image: "nginx", Replicas: 1, Domain: "shop.example.com", Port: 80})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.CreateService(ctx, domain.Service{ProjectID: p.ID, Name: "worker", Kind: domain.ServiceKindApp, Image: "busybox", Replicas: 1}); err != nil {
		t.Fatal(err)
	}

	if _, err := c.TraefikConfig(ctx, domain.LocalServerID, "nope"); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("bad token: %v", err)
	}
	token, err := c.providerToken(ctx, domain.LocalServerID)
	if err != nil {
		t.Fatal(err)
	}
	if other, _ := c.providerToken(ctx, "01OTHER"); other == token {
		t.Error("each server needs its own token")
	}
	if again, _ := c.providerToken(ctx, domain.LocalServerID); again != token {
		t.Error("the token must be stable: it is in Traefik's command")
	}
	if _, err := c.TraefikConfig(ctx, "01OTHER", token); !errors.Is(err, identity.ErrUnauthorized) {
		t.Error("a server's token must not read another's configuration")
	}

	get := func() dynamicConfig {
		t.Helper()
		b, err := c.TraefikConfig(ctx, domain.LocalServerID, token)
		if err != nil {
			t.Fatal(err)
		}
		var cfg dynamicConfig
		if err := json.Unmarshal(b, &cfg); err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	name := "kipitiny-" + strings.ToLower(web.ID)
	cfg := get()
	if len(cfg.HTTP.Routers) != 1 {
		t.Fatalf("routers = %v", cfg.HTTP.Routers)
	}
	r := cfg.HTTP.Routers[name+"-page"]
	if r.Rule != "Host(`shop.example.com`)" || r.Priority != 1 || r.Service != network.PagesService+"@file" || r.TLS["certResolver"] != network.CertResolverTLS ||
		!slices.Equal(r.Middlewares, []string{name + "-page"}) {
		t.Errorf("fallback router = %+v", r)
	}
	if got := string(cfg.HTTP.Middlewares[name+"-page"]["replacePath"]); got != `{"path":"/api/pages/`+web.ID+`"}` {
		t.Errorf("page path = %s", got)
	}

	if err := c.store.SetProjectMaintenance(ctx, p.ID, domain.Maintenance{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	r = get().HTTP.Routers[name+"-maintenance"]
	if r.Priority != maintenancePriority || r.Service != network.PagesService+"@file" || r.Rule != "Host(`shop.example.com`)" {
		t.Errorf("maintenance router = %+v", r)
	}
}
