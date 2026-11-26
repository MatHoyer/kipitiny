package core

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/cloudflare"
	"github.com/kipit-labs/kipitiny/internal/compose"
	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/store"
)

const sharedFile = `
services:
  dashboard:
    image: netbirdio/dashboard:v2.99.0
    expose: ["80"]
    x-kipitiny:
      domain: nb.example.com
  server:
    image: netbirdio/netbird-server:0.83.2
    expose: ["80"]
    ports: ["3478:3478/udp"]
    x-kipitiny:
      domain: nb.example.com
      routes:
        - paths: [/signalexchange.SignalExchange/]
          scheme: h2c
        - paths: [/relay, /api]
`

func TestSharedDomain(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{Traefik: config.Traefik{Enabled: true, HTTPPort: "80", HTTPSPort: "443"}})
	sv, _ := c.store.GetServer(ctx, store.LocalServerID)
	sv.PublicIP = "203.0.113.7"
	if _, err := c.store.UpdateServer(ctx, sv); err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.CreateDomain(ctx, store.Domain{Name: "example.com", Proxied: true}); err != nil {
		t.Fatal(err)
	}
	p, _ := c.store.CreateProject(ctx, store.Project{Name: "nb", ServerID: store.LocalServerID})
	if _, err := c.ApplyCompose(ctx, p.ID, []byte(sharedFile), ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	svcs, _ := c.store.ListServices(ctx, p.ID)
	dash := svcs[0] // by name

	// One record for the hostname, direct: the server publishes STUN.
	wants, err := c.wantedRecords(ctx, []cloudflare.Zone{{ID: "z", Name: "example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if w := wants["nb.example.com"]; w.record.Type != "A" || w.record.Proxied {
		t.Errorf("record = %+v, want a direct A record", w.record)
	}
	if c.routeFor(ctx, dash).behindProxy {
		t.Error("the dashboard shares a direct record: not behind Cloudflare")
	}
	if l := traefikLabels(dash, c.routeFor(ctx, dash)); !strings.Contains(strings.Join(mapValues(l), " "), "Host(`nb.example.com`)") {
		t.Errorf("the catch-all keeps the whole domain: %v", l)
	}

	for name, tc := range map[string]struct{ file, want string }{
		"two catch-alls": {strings.Replace(sharedFile, "      routes:\n        - paths: [/signalexchange.SignalExchange/]\n          scheme: h2c\n        - paths: [/relay, /api]\n", "", 1),
			"already takes the whole domain"},
		"a path twice": {strings.Replace(sharedFile, "      domain: nb.example.com\n  server:", "      domain: nb.example.com\n      routes: [{paths: [/api]}]\n  server:", 1),
			"path /api of \"nb.example.com\" is already routed to dashboard"},
		"analytics twice": {strings.ReplaceAll(sharedFile, "      domain: nb.example.com\n", "      domain: nb.example.com\n      analytics: true\n"),
			"analytics already count"},
	} {
		if _, err := c.ApplyCompose(ctx, p.ID, []byte(tc.file), ApplyOptions{DryRun: true}); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}

	// Moving the catch-all from one service to the other in one file.
	noPorts := strings.Replace(sharedFile, "    ports: [\"3478:3478/udp\"]\n", "", 1)
	swapped := strings.NewReplacer("dashboard:", "tmp:", "server:", "dashboard:").Replace(noPorts)
	swapped = strings.Replace(swapped, "tmp:", "server:", 1)
	if _, err := c.ApplyCompose(ctx, p.ID, []byte(swapped), ApplyOptions{DryRun: true}); err != nil {
		t.Errorf("roles swapped in one file: %v", err)
	}

	// Through the API: same rules, checked against the store.
	if _, err := c.CreateService(ctx, p.ID, ServiceInput{Name: "web", Image: "nginx", Domain: "nb.example.com", Port: 80}); !errors.Is(err, ErrInvalid) {
		t.Errorf("third catch-all: %v", err)
	}
	if _, err := c.CreateService(ctx, p.ID, ServiceInput{Name: "web", Image: "nginx", Domain: "nb.example.com", Port: 80,
		Routes: []store.Route{{Paths: []string{"/web"}}}}); err != nil {
		t.Errorf("another path: %v", err)
	}
	other, _ := c.store.CreateProject(ctx, store.Project{Name: "other", ServerID: store.LocalServerID})
	_, err = c.CreateService(ctx, other.ID, ServiceInput{Name: "x", Image: "nginx", Domain: "nb.example.com", Port: 80, Routes: []store.Route{{Paths: []string{"/x"}}}})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "in project nb") {
		t.Errorf("another project: %v", err)
	}
	if _, err := c.ApplyCompose(ctx, other.ID, []byte(noPorts), ApplyOptions{DryRun: true}); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "in project nb") {
		t.Errorf("another project's file: %v", err)
	}
}

func TestSharedDomainTunnel(t *testing.T) {
	ctx := context.Background()
	tok := base64.StdEncoding.EncodeToString([]byte(`{"a":"acc","t":"tid","s":"x"}`))
	c := newTestCore(t, config.Config{Tunnel: config.Tunnel{Token: tok}, Traefik: config.Traefik{Enabled: true, HTTPPort: "80", HTTPSPort: "443"}})
	p, _ := c.store.CreateProject(ctx, store.Project{Name: "nb", ServerID: store.LocalServerID})
	_, err := c.ApplyCompose(ctx, p.ID, []byte(sharedFile), ApplyOptions{DryRun: true})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "behind a Cloudflare tunnel") {
		t.Errorf("published ports next to HTTP behind a tunnel: %v", err)
	}
}

func TestPreviewSharedDomain(t *testing.T) {
	f, _, err := compose.Parse([]byte(sharedFile+"  worker:\n    image: busybox\n"), compose.Vars{})
	if err != nil {
		t.Fatal(err)
	}
	f.Services["web"] = compose.Service{Image: "nginx", X: compose.Ext{Domain: "www.example.com", Port: 80}}
	if _, err := previewFile(&f, store.Project{Name: "nb"}, "pr-1", store.PreviewSettings{BaseDomain: "pr.example.com"}, nil); err != nil {
		t.Fatal(err)
	}
	if d, s := f.Services["dashboard"].X.Domain, f.Services["server"].X.Domain; d != s || d != "pr-1-dashboard.pr.example.com" {
		t.Errorf("preview domains: dashboard %s, server %s; want one shared", d, s)
	}
	if f.Services["web"].X.Domain != "pr-1-web.pr.example.com" {
		t.Errorf("web = %s", f.Services["web"].X.Domain)
	}
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
