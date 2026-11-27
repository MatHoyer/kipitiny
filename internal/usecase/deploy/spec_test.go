package deploy

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/usecase/network"
)

func TestValidateService(t *testing.T) {
	ok := domain.Service{Image: "nginx:1.27", Replicas: 1}
	tests := []struct {
		name    string
		mutate  func(*domain.Service)
		wantErr bool
	}{
		{"private service", func(*domain.Service) {}, false},
		{"public service", func(s *domain.Service) { s.Domain, s.Port = "shop.example.com", 80 }, false},
		{"localhost subdomain", func(s *domain.Service) { s.Domain, s.Port = "shop.localhost", 80 }, false},
		{"registry image", func(s *domain.Service) { s.Image = "ghcr.io/org/app@sha256:" + sha }, false},
		{"empty image", func(s *domain.Service) { s.Image = "" }, true},
		{"uppercase image", func(s *domain.Service) { s.Image = "Nginx" }, true},
		{"domain without port", func(s *domain.Service) { s.Domain = "shop.example.com" }, true},
		{"bad domain", func(s *domain.Service) { s.Domain, s.Port = "shop_example.com", 80 }, true},
		{"domain with scheme", func(s *domain.Service) { s.Domain, s.Port = "https://shop.example.com", 80 }, true},
		{"zero replicas", func(s *domain.Service) { s.Replicas = 0 }, true},
		{"too many replicas", func(s *domain.Service) { s.Replicas = maxReplicas + 1 }, true},
		{"bad env key", func(s *domain.Service) { s.Env = map[string]string{"A-B": "1"} }, true},
		{"good env key", func(s *domain.Service) { s.Env = map[string]string{"_A1": "1"} }, false},
		{"cpu limit", func(s *domain.Service) { s.CPUs = 0.5 }, false},
		{"tiny cpu limit", func(s *domain.Service) { s.CPUs = 0.001 }, true},
		{"negative cpu limit", func(s *domain.Service) { s.CPUs = -1 }, true},
		{"icon", func(s *domain.Service) { s.Icon = "home-assistant" }, false},
		{"uppercase icon", func(s *domain.Service) { s.Icon = "Ghost" }, true},
		{"icon path", func(s *domain.Service) { s.Icon = "../ghost" }, true},
		{"host network", func(s *domain.Service) { s.HostNetwork, s.Port, s.DockerSocket = true, 45876, "ro" }, false},
		{"host network replicas", func(s *domain.Service) { s.HostNetwork, s.Replicas = true, 2 }, true},
		{"host network domain", func(s *domain.Service) { s.HostNetwork, s.Domain, s.Port = true, "a.example.com", 80 }, true},
		{"host network ports", func(s *domain.Service) {
			s.HostNetwork, s.PublishedPorts = true, []domain.PublishedPort{{HostPort: 9000, ContainerPort: 9000, Protocol: "tcp"}}
		}, true},
		{"bad socket mode", func(s *domain.Service) { s.DockerSocket = "yes" }, true},
		{"host network on a network", func(s *domain.Service) { s.HostNetwork, s.Networks = true, []string{"N1"} }, true},
		{"database socket", func(s *domain.Service) {
			s.Kind, s.Image, s.DockerSocket = domain.ServiceKindPostgres, "postgres:17", "ro"
		}, true},
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
	p := domain.Project{ID: "P1", Name: "shop", Env: map[string]string{"SHARED": "s"}}
	svc := domain.Service{
		ID: "01ABC", ProjectID: "P1", Name: "web", Image: "nginx", Replicas: 2,
		Env: map[string]string{"B": "2", "A": "1", "C": "{{ project.SHARED }}"},
	}

	spec := containerSpec(p, svc, envSources{project: p.Env}, "D1", 2, network.Routing{Resolver: network.CertResolverTLS})
	if spec.Name != "shop-web-2-d1" {
		t.Errorf("name = %q", spec.Name)
	}
	if !slices.Equal(spec.Config.Env, []string{"A=1", "B=2", "C=s"}) {
		t.Errorf("env = %v, want sorted and resolved", spec.Config.Env)
	}
	l := spec.Config.Labels
	if l[domain.LabelProject] != "P1" || l[domain.LabelService] != "01ABC" || l[domain.LabelReplica] != "2" || l[domain.LabelDeploy] != "D1" {
		t.Errorf("labels = %v", l)
	}
	if _, ok := l["traefik.enable"]; ok {
		t.Error("private service must not be exposed to traefik")
	}
	eps := spec.NetworkingConfig.EndpointsConfig
	if len(eps) != 1 || !slices.Equal(eps[domain.ProjectNetwork("P1")].Aliases, []string{"web"}) {
		t.Errorf("endpoints = %v", eps)
	}

	svc.Networks = []string{"N1"}
	spec = containerSpec(p, svc, envSources{project: p.Env}, "D1", 1, network.Routing{})
	eps = spec.NetworkingConfig.EndpointsConfig
	if len(eps) != 2 || !slices.Equal(eps[domain.CustomNetwork("N1")].Aliases, []string{"shop-web"}) {
		t.Errorf("endpoints with a network created by hand = %v", eps)
	}
	svc.Networks = nil

	svc.Domain, svc.Port = "shop.example.com", 8080
	spec = containerSpec(p, svc, envSources{project: p.Env}, "D1", 1, network.Routing{Resolver: network.CertResolverTLS})
	l = spec.Config.Labels
	if l["traefik.enable"] != "true" || l["traefik.docker.network"] != domain.ProxyNetwork {
		t.Errorf("traefik labels missing: %v", l)
	}
	r := routerName(t, l)
	if !strings.HasPrefix(r, "kipitiny-01abc-") || l["traefik.http.routers."+r+".rule"] != "Host(`shop.example.com`)" {
		t.Errorf("router %s rule = %q", r, l["traefik.http.routers."+r+".rule"])
	}
	if l["traefik.http.services.kipitiny-01abc.loadbalancer.server.port"] != "8080" {
		t.Error("service port label wrong")
	}
	if l["traefik.http.routers."+r+".tls.certresolver"] != "letsencrypt" {
		t.Error("public domain must use the ACME resolver")
	}
	if l["traefik.http.routers."+r+".middlewares"] != "kipitiny-01abc-retry@docker" {
		t.Error("retry middleware missing")
	}
	l = containerSpec(p, svc, envSources{project: p.Env}, "D1", 1, network.Routing{}).Config.Labels
	r = routerName(t, l)
	if _, acme := l["traefik.http.routers."+r+".tls.certresolver"]; acme || l["traefik.http.routers."+r+".tls"] != "true" {
		t.Error("no resolver must mean Traefik's default certificate")
	}
	if _, ok := spec.NetworkingConfig.EndpointsConfig[domain.ProxyNetwork]; !ok {
		t.Error("public service must join the proxy network")
	}
}

// routerName is the one router labels define.
func routerName(t *testing.T, labels map[string]string) string {
	t.Helper()
	var out []string
	for k := range labels {
		if r, ok := strings.CutPrefix(k, "traefik.http.routers."); ok && strings.HasSuffix(r, ".rule") {
			out = append(out, strings.TrimSuffix(r, ".rule"))
		}
	}
	if len(out) != 1 {
		t.Fatalf("routers = %v, want one", out)
	}
	return out[0]
}

func TestMasked(t *testing.T) {
	s := domain.Service{Env: map[string]string{"SECRET": "hunter2", "VAR": "v"}, Secrets: []string{"SECRET"}}
	m := masked(s)
	if m.Env["SECRET"] != identity.SecretMask || m.Env["VAR"] != "v" {
		t.Errorf("env = %v, want the secret masked and the variable readable", m.Env)
	}
	if s.Env["SECRET"] != "hunter2" {
		t.Error("masking mutated the original map")
	}
	if masked(domain.Service{}).Env == nil {
		t.Error("nil env should serialize as {}")
	}
}

func TestHostAccessContainerSpec(t *testing.T) {
	p := domain.Project{ID: "P1", Name: "mon"}
	svc := domain.Service{ID: "01ABC", ProjectID: "P1", Name: "agent", Image: "henrygd/beszel-agent", Replicas: 1, HostNetwork: true, DockerSocket: "ro"}
	spec := containerSpec(p, svc, envSources{}, "D1", 1, network.Routing{DockerSocket: "/run/docker.sock"})
	if spec.HostConfig.NetworkMode != "host" || len(spec.NetworkingConfig.EndpointsConfig) != 0 {
		t.Errorf("network = %q, endpoints %v", spec.HostConfig.NetworkMode, spec.NetworkingConfig.EndpointsConfig)
	}
	m := spec.HostConfig.Mounts
	if len(m) != 1 || m[0].Source != "/run/docker.sock" || m[0].Target != "/var/run/docker.sock" || !m[0].ReadOnly {
		t.Errorf("mounts = %+v", m)
	}
}
