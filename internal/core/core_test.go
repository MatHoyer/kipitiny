package core

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestValidateService(t *testing.T) {
	ok := store.Service{Image: "nginx:1.27", Replicas: 1}
	tests := []struct {
		name    string
		mutate  func(*store.Service)
		wantErr bool
	}{
		{"private service", func(*store.Service) {}, false},
		{"public service", func(s *store.Service) { s.Domain, s.Port = "shop.example.com", 80 }, false},
		{"localhost subdomain", func(s *store.Service) { s.Domain, s.Port = "shop.localhost", 80 }, false},
		{"registry image", func(s *store.Service) { s.Image = "ghcr.io/org/app@sha256:" + sha }, false},
		{"empty image", func(s *store.Service) { s.Image = "" }, true},
		{"uppercase image", func(s *store.Service) { s.Image = "Nginx" }, true},
		{"domain without port", func(s *store.Service) { s.Domain = "shop.example.com" }, true},
		{"bad domain", func(s *store.Service) { s.Domain, s.Port = "shop_example.com", 80 }, true},
		{"domain with scheme", func(s *store.Service) { s.Domain, s.Port = "https://shop.example.com", 80 }, true},
		{"zero replicas", func(s *store.Service) { s.Replicas = 0 }, true},
		{"too many replicas", func(s *store.Service) { s.Replicas = maxReplicas + 1 }, true},
		{"bad env key", func(s *store.Service) { s.Env = map[string]string{"A-B": "1"} }, true},
		{"good env key", func(s *store.Service) { s.Env = map[string]string{"_A1": "1"} }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := ok
			tt.mutate(&s)
			err := validateService(s)
			if (err != nil) != tt.wantErr {
				t.Fatalf("got %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalid) {
				t.Fatalf("error should wrap ErrInvalid: %v", err)
			}
		})
	}
}

const sha = "0000000000000000000000000000000000000000000000000000000000000000"

func TestAppContainerSpec(t *testing.T) {
	p := store.Project{ID: "P1", Name: "shop", Env: map[string]string{"SHARED": "s"}}
	svc := store.Service{
		ID: "01ABC", ProjectID: "P1", Name: "web", Image: "nginx", Replicas: 2,
		Env: map[string]string{"B": "2", "A": "1", "C": "{{ project.SHARED }}"},
	}

	spec := containerSpec(p, svc, envSources{project: p.Env}, "D1", 2, certResolver)
	if spec.Name != "shop-web-2-d1" {
		t.Errorf("name = %q", spec.Name)
	}
	if !slices.Equal(spec.Config.Env, []string{"A=1", "B=2", "C=s"}) {
		t.Errorf("env = %v, want sorted and resolved", spec.Config.Env)
	}
	l := spec.Config.Labels
	if l[docker.LabelProject] != "P1" || l[docker.LabelService] != "01ABC" || l[docker.LabelReplica] != "2" || l[docker.LabelDeploy] != "D1" {
		t.Errorf("labels = %v", l)
	}
	if _, ok := l["traefik.enable"]; ok {
		t.Error("private service must not be exposed to traefik")
	}
	eps := spec.NetworkingConfig.EndpointsConfig
	if len(eps) != 1 || !slices.Equal(eps[docker.ProjectNetwork("P1")].Aliases, []string{"web"}) {
		t.Errorf("endpoints = %v", eps)
	}

	svc.Domain, svc.Port = "shop.example.com", 8080
	spec = containerSpec(p, svc, envSources{project: p.Env}, "D1", 1, certResolver)
	l = spec.Config.Labels
	if l["traefik.enable"] != "true" || l["traefik.docker.network"] != docker.ProxyNetwork {
		t.Errorf("traefik labels missing: %v", l)
	}
	if l["traefik.http.routers.kipitiny-01abc.rule"] != "Host(`shop.example.com`)" {
		t.Errorf("router rule = %q", l["traefik.http.routers.kipitiny-01abc.rule"])
	}
	if l["traefik.http.services.kipitiny-01abc.loadbalancer.server.port"] != "8080" {
		t.Error("service port label wrong")
	}
	if l["traefik.http.routers.kipitiny-01abc.tls.certresolver"] != "letsencrypt" {
		t.Error("public domain must use the ACME resolver")
	}
	if l["traefik.http.routers.kipitiny-01abc.middlewares"] != "kipitiny-01abc-retry@docker" {
		t.Error("retry middleware missing")
	}
	l = containerSpec(p, svc, envSources{project: p.Env}, "D1", 1, "").Config.Labels
	if _, acme := l["traefik.http.routers.kipitiny-01abc.tls.certresolver"]; acme || l["traefik.http.routers.kipitiny-01abc.tls"] != "true" {
		t.Error("no resolver must mean Traefik's default certificate")
	}
	if _, ok := spec.NetworkingConfig.EndpointsConfig[docker.ProxyNetwork]; !ok {
		t.Error("public service must join the proxy network")
	}
}

func TestMasked(t *testing.T) {
	s := store.Service{Env: map[string]string{"SECRET": "hunter2", "VAR": "v"}, Secrets: []string{"SECRET"}}
	m := masked(s)
	if m.Env["SECRET"] != SecretMask || m.Env["VAR"] != "v" {
		t.Errorf("env = %v, want the secret masked and the variable readable", m.Env)
	}
	if s.Env["SECRET"] != "hunter2" {
		t.Error("masking mutated the original map")
	}
	if masked(store.Service{}).Env == nil {
		t.Error("nil env should serialize as {}")
	}
}

func TestTraefikManagerRoute(t *testing.T) {
	c := &Core{cfg: config.Config{Domain: "kipitiny.example.com"}}
	opts, files := c.traefikSpec("/var/run/docker.sock", traefikOpts{})
	if len(files) != 0 || slices.ContainsFunc(opts.Config.Cmd, func(a string) bool { return strings.HasPrefix(a, "--providers.file") }) {
		t.Error("no manager URL must mean no file provider")
	}

	opts, files = c.traefikSpec("/var/run/docker.sock", traefikOpts{ManagerURL: "http://kipitiny-manager:3000", ManagerResolver: certResolver})
	if len(files) != 1 || !slices.Contains(opts.Config.Cmd, "--providers.file.filename="+files[0].Path) {
		t.Fatalf("file provider not wired: %v", opts.Config.Cmd)
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
	r := route.HTTP.Routers[managerAlias]
	if r.Rule != "Host(`kipitiny.example.com`)" || r.TLS.CertResolver != certResolver {
		t.Errorf("router = %+v", r)
	}
	if s := route.HTTP.Services[managerAlias].LoadBalancer.Servers; len(s) != 1 || s[0].URL != "http://kipitiny-manager:3000" {
		t.Errorf("servers = %+v", s)
	}
	if len(opts.HostConfig.ExtraHosts) != 0 {
		t.Error("container upstream needs no host-gateway")
	}

	opts, _ = c.traefikSpec("/var/run/docker.sock", traefikOpts{ManagerURL: "http://host.docker.internal:3000", ManagerResolver: certResolver})
	if !slices.Equal(opts.HostConfig.ExtraHosts, []string{"host.docker.internal:host-gateway"}) {
		t.Errorf("extra hosts = %v", opts.HostConfig.ExtraHosts)
	}
}

func TestTunnelMode(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{Domain: "kipitiny.example.com", Tunnel: config.Tunnel{Token: "tok", Image: "cloudflare/cloudflared"}})
	worker, err := c.store.CreateServer(ctx, store.Server{Name: "w1", Kind: store.ServerSSH, Host: "w1", Port: 22})
	if err != nil {
		t.Fatal(err)
	}
	if !c.viaTunnel(ctx, store.LocalServerID) || !c.viaTunnel(ctx, "") || c.viaTunnel(ctx, worker.ID) {
		t.Error("the environment token only serves the manager's own server")
	}
	worker.TunnelToken = "worker-tok"
	if _, err := c.store.UpdateServer(ctx, worker); err != nil {
		t.Fatal(err)
	}
	if !c.viaTunnel(ctx, worker.ID) || c.tunnelToken(worker) != "worker-tok" {
		t.Error("a server's own token puts it behind its tunnel")
	}

	opts, files := c.traefikSpec("/var/run/docker.sock", traefikOpts{ManagerURL: "http://kipitiny-manager:3000", Tunnel: true})
	if len(opts.HostConfig.PortBindings) != 0 {
		t.Errorf("tunnel mode must publish no ports: %v", opts.HostConfig.PortBindings)
	}
	if slices.ContainsFunc(opts.Config.Cmd, func(a string) bool { return strings.Contains(a, "certificatesresolvers") }) {
		t.Error("tunnel mode must not run ACME")
	}
	if strings.Contains(string(files[0].Content), "certResolver") {
		t.Error("manager route must not reference the ACME resolver")
	}

	l := traefikLabels(store.Service{ID: "01ABC", Domain: "shop.example.com", Port: 80}, "")
	if _, acme := l["traefik.http.routers.kipitiny-01abc.tls.certresolver"]; acme || l["traefik.http.routers.kipitiny-01abc.tls"] != "true" {
		t.Errorf("tunnel labels = %v", l)
	}

	spec := c.tunnelSpec("tok")
	if !slices.Contains(spec.Config.Env, "TUNNEL_TOKEN=tok") || slices.Contains(spec.Config.Cmd, "tok") {
		t.Error("token must be passed through the environment only")
	}
	if _, ok := spec.NetworkingConfig.EndpointsConfig[docker.ProxyNetwork]; !ok {
		t.Error("cloudflared must reach Traefik on the proxy network")
	}
}
