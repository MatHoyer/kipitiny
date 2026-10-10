package core

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestSetMaintenanceValidation(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	db, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "db", Kind: store.ServiceKindPostgres, Image: "postgres", Replicas: 1})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "worker", Kind: store.ServiceKindApp, Image: "busybox", Replicas: 1})
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		id string
		in MaintenanceInput
	}{
		"database":       {db.ID, MaintenanceInput{Title: "x"}},
		"no domain":      {worker.ID, MaintenanceInput{Enabled: true}},
		"bad range":      {worker.ID, MaintenanceInput{AllowIPs: []string{"10.0.0.0/33"}}},
		"long title":     {worker.ID, MaintenanceInput{Title: strings.Repeat("a", maxPageTitle+1)}},
		"huge page":      {worker.ID, MaintenanceInput{HTML: strings.Repeat("a", maxPageHTML+1)}},
		"not utf-8 html": {worker.ID, MaintenanceInput{HTML: "\xff"}},
	} {
		if _, err := c.SetMaintenance(ctx, tc.id, tc.in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestMaintenancePage(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1, Domain: "shop.example.com", Port: 80})
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

	m := store.Maintenance{Enabled: true, Title: "Upgrading <db>", Message: "Back at 3pm."}
	if err := c.store.SetServiceMaintenance(ctx, svc.ID, m); err != nil {
		t.Fatal(err)
	}
	body := string(c.MaintenancePage(ctx, svc.ID, "").Body)
	if !strings.Contains(body, "<h1>Upgrading &lt;db&gt;</h1>") || !strings.Contains(body, "Back at 3pm.") {
		t.Errorf("custom text:\n%s", body)
	}
	m = store.Maintenance{Enabled: true}
	if err := c.store.SetServiceMaintenance(ctx, svc.ID, m); err != nil {
		t.Fatal(err)
	}
	if body := string(c.MaintenancePage(ctx, svc.ID, "").Body); !strings.Contains(body, "shop is down for maintenance") {
		t.Errorf("maintenance mode text:\n%s", body)
	}

	m.HTML = "<h1>Gone fishing</h1>"
	if err := c.store.SetServiceMaintenance(ctx, svc.ID, m); err != nil {
		t.Fatal(err)
	}
	if page := c.MaintenancePage(ctx, svc.ID, ""); !page.Custom || string(page.Body) != m.HTML {
		t.Errorf("custom HTML: %+v", page)
	}
	// A settings update (the form, a git sync) keeps it.
	svc.Image = "nginx:2"
	if _, err := c.store.UpdateService(ctx, svc); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.store.GetService(ctx, svc.ID); got.Maintenance.HTML != m.HTML || !got.Maintenance.Enabled {
		t.Errorf("maintenance after update = %+v", got.Maintenance)
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
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	web, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "nginx", Replicas: 1, Domain: "shop.example.com", Port: 80})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "worker", Kind: store.ServiceKindApp, Image: "busybox", Replicas: 1}); err != nil {
		t.Fatal(err)
	}

	if _, err := c.TraefikConfig(ctx, store.LocalServerID, "nope"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("bad token: %v", err)
	}
	token, err := c.providerToken(ctx, store.LocalServerID)
	if err != nil {
		t.Fatal(err)
	}
	if other, _ := c.providerToken(ctx, "01OTHER"); other == token {
		t.Error("each server needs its own token")
	}
	if again, _ := c.providerToken(ctx, store.LocalServerID); again != token {
		t.Error("the token must be stable: it is in Traefik's command")
	}
	if _, err := c.TraefikConfig(ctx, "01OTHER", token); !errors.Is(err, ErrUnauthorized) {
		t.Error("a server's token must not read another's configuration")
	}

	get := func() dynamicConfig {
		t.Helper()
		b, err := c.TraefikConfig(ctx, store.LocalServerID, token)
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
	if r.Rule != "Host(`shop.example.com`)" || r.Priority != 1 || r.Service != pagesService+"@file" || r.TLS["certResolver"] != certResolver ||
		!slices.Equal(r.Middlewares, []string{name + "-page"}) {
		t.Errorf("fallback router = %+v", r)
	}
	if got := string(cfg.HTTP.Middlewares[name+"-page"]["replacePath"]); got != `{"path":"/api/pages/`+web.ID+`"}` {
		t.Errorf("page path = %s", got)
	}

	if err := c.store.SetServiceMaintenance(ctx, web.ID, store.Maintenance{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	r = get().HTTP.Routers[name+"-maintenance"]
	if r.Priority != maintenancePriority || r.Service != pagesService+"@file" || r.Rule != "Host(`shop.example.com`)" {
		t.Errorf("maintenance router = %+v", r)
	}
}

func TestTraefikPages(t *testing.T) {
	c := &Core{cfg: config.Config{Domain: "kipitiny.example.com"}}
	o := traefikOpts{
		ManagerURL: "http://kipitiny-manager:3000", ManagerResolver: certResolver,
		Pages: &pagesOpts{Upstream: "http://kipitiny-manager:3000", ServerID: "01LOCAL", Token: "tok"},
	}
	opts, files := c.traefikSpec("/var/run/docker.sock", o)
	for _, a := range []string{
		"--entrypoints.websecure.http.middlewares=" + pagesDown + "@file",
		"--providers.http.endpoint=http://kipitiny-manager:3000/api/pages/traefik/01LOCAL",
		"--providers.http.headers.Authorization=Bearer tok",
	} {
		if !slices.Contains(opts.Config.Cmd, a) {
			t.Errorf("missing %s in %v", a, opts.Config.Cmd)
		}
	}
	if len(files) != 1 {
		t.Fatalf("files = %d", len(files))
	}
	var cfg struct {
		HTTP struct {
			Routers     map[string]any
			Services    map[string]any
			Middlewares map[string]any
		}
	}
	if err := json.Unmarshal(files[0].Content, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Routers[managerAlias] == nil || cfg.HTTP.Services[managerAlias] == nil || cfg.HTTP.Services[pagesService] == nil || cfg.HTTP.Middlewares[pagesDown] == nil {
		t.Errorf("file provider must keep the manager route next to the pages:\n%s", files[0].Content)
	}

	// A remote server: no manager route, pages through the public domain.
	o = traefikOpts{Pages: &pagesOpts{Upstream: "https://kipitiny.example.com", ServerID: "01W", Token: "tok"}}
	opts, files = c.traefikSpec("/var/run/docker.sock", o)
	if len(files) != 1 || strings.Contains(string(files[0].Content), managerAlias) || len(opts.HostConfig.ExtraHosts) != 0 {
		t.Errorf("remote: %s %v", files[0].Content, opts.HostConfig.ExtraHosts)
	}
}
