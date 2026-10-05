package core

import (
	"context"
	"encoding/base64"
	"errors"
	"slices"
	"testing"

	"github.com/moby/moby/api/types/network"

	"github.com/MatHoyer/kipitiny/internal/cloudflare"
	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestNormalizePorts(t *testing.T) {
	got := normalizePorts([]store.PublishedPort{{HostPort: 25565}, {HostPort: 19132, ContainerPort: 19133, Protocol: " UDP "}})
	want := []store.PublishedPort{{HostPort: 25565, ContainerPort: 25565, Protocol: "tcp"}, {HostPort: 19132, ContainerPort: 19133, Protocol: "udp"}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %+v", got)
	}
	if got := normalizePorts(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil: %#v", got)
	}
}

func TestValidatePorts(t *testing.T) {
	tcp := func(host, ctr int) store.PublishedPort {
		return store.PublishedPort{HostPort: host, ContainerPort: ctr, Protocol: "tcp"}
	}
	tests := []struct {
		name     string
		replicas int
		ports    []store.PublishedPort
		wantErr  bool
	}{
		{"none", 3, nil, false},
		{"minecraft", 1, []store.PublishedPort{tcp(25565, 25565), {HostPort: 25565, ContainerPort: 25565, Protocol: "udp"}}, false},
		{"two host ports to one container port", 1, []store.PublishedPort{tcp(25565, 25565), tcp(25566, 25565)}, false},
		{"replicas", 2, []store.PublishedPort{tcp(25565, 25565)}, true},
		{"protocol", 1, []store.PublishedPort{{HostPort: 1, ContainerPort: 1, Protocol: "sctp"}}, true},
		{"host port range", 1, []store.PublishedPort{tcp(70000, 80)}, true},
		{"container port range", 1, []store.PublishedPort{tcp(8080, 0)}, true},
		{"duplicate", 1, []store.PublishedPort{tcp(25565, 25565), tcp(25565, 25566)}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePorts(store.Service{Replicas: tt.replicas, PublishedPorts: tt.ports})
			if (err != nil) != tt.wantErr {
				t.Fatalf("got %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalid) {
				t.Fatalf("error should wrap ErrInvalid: %v", err)
			}
		})
	}
}

func TestDatabaseRefusesPorts(t *testing.T) {
	svc := redisService()
	svc.PublishedPorts = []store.PublishedPort{{HostPort: 6379, ContainerPort: 6379, Protocol: "tcp"}}
	if err := validateService(svc); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v, want ErrInvalid", err)
	}
}

func TestCheckPortsFree(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{Traefik: config.Traefik{Enabled: true, HTTPPort: "80", HTTPSPort: "443"}})
	remote, err := c.store.CreateServer(ctx, store.Server{Name: "w1", Kind: store.ServerSSH, Host: "w1", Port: 22})
	if err != nil {
		t.Fatal(err)
	}
	local, _ := c.store.CreateProject(ctx, store.Project{Name: "a", ServerID: store.LocalServerID})
	far, _ := c.store.CreateProject(ctx, store.Project{Name: "b", ServerID: remote.ID})
	mc := []store.PublishedPort{{HostPort: 25565, ContainerPort: 25565, Protocol: "tcp"}}
	existing, err := c.store.CreateService(ctx, store.Service{ProjectID: local.ID, Name: "mc", Kind: store.ServiceKindApp, Image: "x", Replicas: 1, PublishedPorts: mc})
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name  string
		svc   store.Service
		taken bool
	}{
		{"same server", store.Service{ProjectID: local.ID, PublishedPorts: mc}, true},
		{"other server", store.Service{ProjectID: far.ID, PublishedPorts: mc}, false},
		{"other protocol", store.Service{ProjectID: local.ID, PublishedPorts: []store.PublishedPort{{HostPort: 25565, ContainerPort: 25565, Protocol: "udp"}}}, false},
		{"itself", existing, false},
		{"traefik", store.Service{ProjectID: far.ID, PublishedPorts: []store.PublishedPort{{HostPort: 443, ContainerPort: 8443, Protocol: "tcp"}}}, true},
		{"udp 443", store.Service{ProjectID: far.ID, PublishedPorts: []store.PublishedPort{{HostPort: 443, ContainerPort: 443, Protocol: "udp"}}}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := c.checkPortsFree(ctx, tt.svc)
			if tt.taken != (err != nil) {
				t.Fatalf("got %v, taken %v", err, tt.taken)
			}
			if err != nil && !errors.Is(err, ErrInvalid) {
				t.Fatalf("error should wrap ErrInvalid: %v", err)
			}
		})
	}
}

func TestPublishedPortsSpec(t *testing.T) {
	p := store.Project{ID: "P1", Name: "games"}
	svc := store.Service{ID: "01ABC", ProjectID: "P1", Name: "mc", Kind: store.ServiceKindApp, Image: "itzg/minecraft-server", Replicas: 1,
		PublishedPorts: []store.PublishedPort{
			{HostPort: 25565, ContainerPort: 25565, Protocol: "tcp"},
			{HostPort: 25566, ContainerPort: 25565, Protocol: "tcp"},
			{HostPort: 19132, ContainerPort: 19132, Protocol: "udp"},
		}}
	spec := containerSpec(p, svc, envSources{}, "D1", 1, route{})
	tcp, udp := network.MustParsePort("25565/tcp"), network.MustParsePort("19132/udp")
	if _, ok := spec.Config.ExposedPorts[tcp]; !ok || len(spec.Config.ExposedPorts) != 2 {
		t.Errorf("exposed = %v", spec.Config.ExposedPorts)
	}
	b := spec.HostConfig.PortBindings
	if len(b[tcp]) != 2 || b[tcp][0].HostPort != "25565" || b[tcp][1].HostPort != "25566" || len(b[udp]) != 1 || b[udp][0].HostPort != "19132" {
		t.Errorf("bindings = %v", b)
	}
	if _, public := spec.NetworkingConfig.EndpointsConfig["kipitiny-proxy"]; public {
		t.Error("a service without a domain must stay off the proxy network")
	}

	if got := probePort(svc); got != 25565 {
		t.Errorf("probe port = %d", got)
	}
	svc.Port = 8123 // e.g. a web map, routed by Traefik
	if got := probePort(svc); got != 8123 {
		t.Errorf("probe port with HTTP port = %d", got)
	}
	if got := probePort(store.Service{PublishedPorts: []store.PublishedPort{{HostPort: 1, ContainerPort: 1, Protocol: "udp"}}}); got != 0 {
		t.Errorf("udp only: %d", got)
	}
}

func TestPublishedPortsDomain(t *testing.T) {
	ctx := context.Background()
	tok := base64.StdEncoding.EncodeToString([]byte(`{"a":"acc","t":"tid","s":"x"}`))
	c := newTestCore(t, config.Config{Tunnel: config.Tunnel{Token: tok}, Traefik: config.Traefik{Enabled: true, HTTPPort: "80", HTTPSPort: "443"}})
	sv, err := c.store.GetServer(ctx, store.LocalServerID)
	if err != nil {
		t.Fatal(err)
	}
	sv.PublicIP = "203.0.113.7"
	if _, err := c.store.UpdateServer(ctx, sv); err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.CreateDomain(ctx, store.Domain{Name: "example.com", Proxied: true}); err != nil {
		t.Fatal(err)
	}
	p, _ := c.store.CreateProject(ctx, store.Project{Name: "games", ServerID: store.LocalServerID})
	ports := []store.PublishedPort{{HostPort: 25565, ContainerPort: 25565, Protocol: "tcp"}}

	// Behind the tunnel, the domain of a port-only app is a direct record.
	view, err := c.CreateService(ctx, p.ID, ServiceInput{Name: "mc", Image: "itzg/minecraft-server", Domain: "mc.example.com", PublishedPorts: ports})
	if err != nil {
		t.Fatal(err)
	}
	mc := view.Service
	wants, err := c.wantedRecords(ctx, []cloudflare.Zone{{ID: "z", Name: "example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if w := wants["mc.example.com"]; w.tunnel != "" || w.record.Type != "A" || w.record.Content != "203.0.113.7" || w.record.Proxied {
		t.Errorf("record = %+v", w)
	}
	spec := containerSpec(p, mc, envSources{}, "D1", 1, c.routeFor(ctx, mc))
	if spec.Config.Labels["traefik.enable"] != "" {
		t.Errorf("a domain without a container port must not be routed: %v", spec.Config.Labels)
	}
	if _, err := c.SetUptime(ctx, mc.ID, UptimeInput{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("uptime check on a port-only app: %v", err)
	}

	// HTTP on the same domain needs the server's own ports: not behind a tunnel.
	port := 8123
	if _, err := c.UpdateService(ctx, mc.ID, ServicePatch{Port: &port}); !errors.Is(err, ErrInvalid) {
		t.Errorf("HTTP behind the tunnel: got %v", err)
	}
	// A domain still needs some port.
	if _, err := c.CreateService(ctx, p.ID, ServiceInput{Name: "web", Image: "nginx", Domain: "web.example.com"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("domain without any port: got %v", err)
	}

	c.cfg.Tunnel.Token = ""
	mc.Port = port
	if err := c.validate(ctx, mc); err != nil {
		t.Fatalf("HTTP without a tunnel: %v", err)
	}
	if rt := c.routeFor(ctx, mc); rt.behindProxy {
		t.Error("the record of an app with published ports is never proxied")
	}
	if l := containerSpec(p, mc, envSources{}, "D1", 1, c.routeFor(ctx, mc)).Config.Labels; l["traefik.enable"] != "true" {
		t.Errorf("labels = %v", l)
	}
}
