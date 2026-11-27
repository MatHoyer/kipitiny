package network

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestTraefikReadTimeout(t *testing.T) {
	const flag = "--entrypoints.websecure.transport.respondingTimeouts.readTimeout="
	has := func(c *Network) []string {
		opts, _ := c.TraefikSpec("/var/run/docker.sock", TraefikOpts{})
		return slices.DeleteFunc(slices.Clone(opts.Cmd), func(a string) bool { return !strings.HasPrefix(a, flag) })
	}
	if got := has(&Network{cfg: &config.Config{}}); len(got) != 0 {
		t.Errorf("unset: %v (would recreate Traefik on upgrade)", got)
	}
	if got := has(&Network{cfg: &config.Config{Traefik: config.Traefik{ReadTimeout: "0"}}}); !slices.Equal(got, []string{flag + "0"}) {
		t.Errorf("0: %v", got)
	}
}

func TestTraefikManagerRoute(t *testing.T) {
	c := &Network{cfg: &config.Config{Domain: "kipitiny.example.com"}}
	opts, files := c.TraefikSpec("/var/run/docker.sock", TraefikOpts{})
	if len(files) != 0 || slices.ContainsFunc(opts.Cmd, func(a string) bool { return strings.HasPrefix(a, "--providers.file") }) {
		t.Error("no manager URL must mean no file provider")
	}

	opts, files = c.TraefikSpec("/var/run/docker.sock", TraefikOpts{ManagerURL: "http://kipitiny-manager:3000", ManagerResolver: CertResolverTLS})
	if len(files) != 1 || !slices.Contains(opts.Cmd, "--providers.file.filename="+files[0].Path) {
		t.Fatalf("file provider not wired: %v", opts.Cmd)
	}
	var route struct {
		HTTP struct {
			Routers map[string]struct {
				Rule string
				TLS  struct{ CertResolver string } `json:"tls"`
			}
			Services map[string]struct {
				LoadBalancer struct{ Servers []struct{ URL string } }
			}
		}
	}
	if err := json.Unmarshal(files[0].Content, &route); err != nil {
		t.Fatal(err)
	}
	r := route.HTTP.Routers[ManagerAlias]
	if r.Rule != "Host(`kipitiny.example.com`)" || r.TLS.CertResolver != CertResolverTLS {
		t.Errorf("router = %+v", r)
	}
	if s := route.HTTP.Services[ManagerAlias].LoadBalancer.Servers; len(s) != 1 || s[0].URL != "http://kipitiny-manager:3000" {
		t.Errorf("servers = %+v", s)
	}
	if len(opts.ExtraHosts) != 0 {
		t.Error("container upstream needs no host-gateway")
	}

	opts, _ = c.TraefikSpec("/var/run/docker.sock", TraefikOpts{ManagerURL: "http://host.docker.internal:3000", ManagerResolver: CertResolverTLS})
	if !slices.Equal(opts.ExtraHosts, []string{"host.docker.internal:host-gateway"}) {
		t.Errorf("extra hosts = %v", opts.ExtraHosts)
	}
}

func TestTunnelMode(t *testing.T) {
	ctx := context.Background()
	c := newTestNetwork(t, config.Config{Domain: "kipitiny.example.com", Tunnel: config.Tunnel{Token: "tok", Image: "cloudflare/cloudflared"}})
	worker, err := c.store.CreateServer(ctx, domain.Server{Name: "w1", Kind: domain.ServerSSH, Host: "w1", Port: 22})
	if err != nil {
		t.Fatal(err)
	}
	if !c.ViaTunnel(ctx, domain.LocalServerID) || !c.ViaTunnel(ctx, "") || c.ViaTunnel(ctx, worker.ID) {
		t.Error("the environment token only serves the manager's own server")
	}
	worker.TunnelToken = "worker-tok"
	if _, err := c.store.UpdateServer(ctx, worker); err != nil {
		t.Fatal(err)
	}
	if !c.ViaTunnel(ctx, worker.ID) || c.TunnelToken(worker) != "worker-tok" {
		t.Error("a server's own token puts it behind its tunnel")
	}

	opts, files := c.TraefikSpec("/var/run/docker.sock", TraefikOpts{ManagerURL: "http://kipitiny-manager:3000", Tunnel: true})
	if len(opts.PortBindings) != 0 {
		t.Errorf("tunnel mode must publish no ports: %v", opts.PortBindings)
	}
	if slices.ContainsFunc(opts.Cmd, func(a string) bool { return strings.Contains(a, "certificatesresolvers") }) {
		t.Error("tunnel mode must not run ACME")
	}
	if strings.Contains(string(files[0].Content), "certResolver") {
		t.Error("manager route must not reference the ACME resolver")
	}

	l := TraefikLabels(domain.Service{ID: "01ABC", Domain: "shop.example.com", Port: 80}, Routing{})
	r := routerName(t, l)
	if _, acme := l["traefik.http.routers."+r+".tls.certresolver"]; acme || l["traefik.http.routers."+r+".tls"] != "true" {
		t.Errorf("tunnel labels = %v", l)
	}

	spec := c.tunnelSpec("tok")
	if !slices.Contains(spec.Env, "TUNNEL_TOKEN=tok") || slices.Contains(spec.Cmd, "tok") {
		t.Error("token must be passed through the environment only")
	}
	if _, ok := spec.Networks[domain.ProxyNetwork]; !ok {
		t.Error("cloudflared must reach Traefik on the proxy network")
	}
}

func TestTraefikPages(t *testing.T) {
	c := &Network{cfg: &config.Config{Domain: "kipitiny.example.com"}}
	o := TraefikOpts{
		ManagerURL: "http://kipitiny-manager:3000", ManagerResolver: CertResolverTLS,
		Pages: &PagesOpts{Upstream: "http://kipitiny-manager:3000", ServerID: "01LOCAL", Token: "tok"},
	}
	opts, files := c.TraefikSpec("/var/run/docker.sock", o)
	for _, a := range []string{
		"--entrypoints.websecure.http.middlewares=" + PagesDown + "@file",
		"--providers.http.endpoint=http://kipitiny-manager:3000/api/pages/traefik/01LOCAL",
		"--providers.http.headers.Authorization=Bearer tok",
	} {
		if !slices.Contains(opts.Cmd, a) {
			t.Errorf("missing %s in %v", a, opts.Cmd)
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
	if cfg.HTTP.Routers[ManagerAlias] == nil || cfg.HTTP.Services[ManagerAlias] == nil || cfg.HTTP.Services[PagesService] == nil || cfg.HTTP.Middlewares[PagesDown] == nil {
		t.Errorf("file provider must keep the manager route next to the pages:\n%s", files[0].Content)
	}

	// A remote server: no manager route, pages through the public domain.
	o = TraefikOpts{Pages: &PagesOpts{Upstream: "https://kipitiny.example.com", ServerID: "01W", Token: "tok"}}
	opts, files = c.TraefikSpec("/var/run/docker.sock", o)
	if len(files) != 1 || strings.Contains(string(files[0].Content), ManagerAlias) || len(opts.ExtraHosts) != 0 {
		t.Errorf("remote: %s %v", files[0].Content, opts.ExtraHosts)
	}
}
