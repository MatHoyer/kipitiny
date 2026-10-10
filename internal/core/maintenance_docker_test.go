package core

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// TestMaintenanceTraefikDocker runs Traefik as the manager configures it in
// front of an app container, and checks visitors get the page when the app
// can't answer and in maintenance mode. Nothing it creates carries
// kipitiny.managed, so a manager on the same daemon leaves it alone.
//
//	KIPITINY_TEST_DOCKER=1 go test ./internal/core/ -run MaintenanceTraefikDocker
func TestMaintenanceTraefikDocker(t *testing.T) {
	if os.Getenv("KIPITINY_TEST_DOCKER") == "" {
		t.Skip("KIPITINY_TEST_DOCKER not set")
	}
	ctx := context.Background()
	dk, err := docker.New()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DataDir: t.TempDir(), Traefik: config.Traefik{Image: "traefik:v3.7", DockerSocket: "/var/run/docker.sock", HTTPPort: "0", HTTPSPort: "0"}}
	c := newTestCore(t, cfg)
	c.pool = docker.NewPool(dk, c.connectServer)
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := c.store.CreateService(ctx, store.Service{ProjectID: p.ID, Name: "web", Kind: store.ServiceKindApp, Image: "busybox", Replicas: 1, Domain: "shop.localhost", Port: 8080})
	if err != nil {
		t.Fatal(err)
	}
	for _, img := range []string{"busybox", cfg.Traefik.Image} {
		if err := dk.EnsureImage(ctx, img, ""); err != nil {
			t.Fatal(err)
		}
	}

	// The manager's pages, as cmd/kipitiny mounts them.
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+PagesPath+"traefik/{server}", func(w http.ResponseWriter, r *http.Request) {
		token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		b, err := c.TraefikConfig(r.Context(), r.PathValue("server"), token)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		_, _ = w.Write(b)
	})
	page := func(w http.ResponseWriter, p Page) {
		w.Header().Set("Retry-After", "300")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write(p.Body)
	}
	mux.HandleFunc(PagesPath+"down", func(w http.ResponseWriter, r *http.Request) {
		page(w, c.MaintenancePage(r.Context(), "", r.URL.Query().Get("url")))
	})
	mux.HandleFunc(PagesPath+"{id}", func(w http.ResponseWriter, r *http.Request) {
		page(w, c.MaintenancePage(r.Context(), r.PathValue("id"), ""))
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	netName := "kipitiny-test-maint-" + strings.ToLower(svc.ID)
	if _, err := dk.NetworkCreate(ctx, netName, client.NetworkCreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dk.RemoveNetwork(context.Background(), netName) })

	// Traefik as ensureTraefik builds it, minus what would make the manager
	// or the real Traefik take it for theirs.
	token, err := c.providerToken(ctx, store.LocalServerID)
	if err != nil {
		t.Fatal(err)
	}
	upstream := "http://" + managerHost + ":" + portOf(t, ln.Addr())
	opts, files := c.traefikSpec(cfg.Traefik.DockerSocket, traefikOpts{Pages: &pagesOpts{Upstream: upstream, ServerID: store.LocalServerID, Token: token}})
	constraint := "tt-" + strings.ToLower(svc.ID)
	for i, a := range opts.Config.Cmd {
		switch {
		case strings.HasPrefix(a, "--providers.docker.constraints="):
			opts.Config.Cmd[i] = "--providers.docker.constraints=Label(`" + constraint + "`,`true`)"
		case strings.HasPrefix(a, "--providers.docker.network="):
			opts.Config.Cmd[i] = "--providers.docker.network=" + netName
		}
	}
	opts.Config.Cmd = append(opts.Config.Cmd, "--providers.http.pollinterval=1s")
	opts.Name = netName + "-traefik"
	opts.Config.Labels = nil
	opts.HostConfig.Mounts = opts.HostConfig.Mounts[:1] // the socket, not the real Traefik's ACME volume
	opts.HostConfig.PortBindings = network.PortMap{network.MustParsePort("443/tcp"): {{HostIP: netip.MustParseAddr("127.0.0.1")}}}
	opts.NetworkingConfig.EndpointsConfig = map[string]*network.EndpointSettings{netName: {}}
	traefikID, err := dk.Run(ctx, opts, files...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dk.RemoveContainer(context.Background(), traefikID, 0) })
	insp, err := dk.ContainerInspect(ctx, traefikID, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var addr string
	for _, b := range insp.Container.NetworkSettings.Ports[network.MustParsePort("443/tcp")] {
		addr = "127.0.0.1:" + b.HostPort
	}
	gateway := ""
	for _, ep := range insp.Container.NetworkSettings.Networks {
		gateway = ep.Gateway.String() // where requests from the host come from
	}

	runApp := func(cmd string) string {
		t.Helper()
		labels := traefikLabels(svc, route{})
		labels["traefik.docker.network"] = netName
		labels[constraint] = "true"
		labels[docker.LabelProject], labels[docker.LabelService] = p.ID, svc.ID
		id, err := dk.Run(ctx, client.ContainerCreateOptions{
			Config:           &container.Config{Image: "busybox", Cmd: []string{"sh", "-c", cmd}, Labels: labels},
			NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{netName: {}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = dk.RemoveContainer(context.Background(), id, 0) })
		return id
	}
	hc := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: svc.Domain},
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		},
	}}
	// expect polls until the app's domain answers status with want in the
	// body: Traefik takes a moment to see containers and configuration.
	expect := func(step string, status int, want string) {
		t.Helper()
		var got string
		for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(300 * time.Millisecond) {
			res, err := hc.Get("https://" + svc.Domain + "/")
			if err != nil {
				got = err.Error()
				continue
			}
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			got = res.Status + " " + string(b)
			if res.StatusCode == status && strings.Contains(string(b), want) {
				if status == http.StatusServiceUnavailable && res.Header.Get("Retry-After") == "" {
					t.Errorf("%s: no Retry-After", step)
				}
				return
			}
		}
		t.Fatalf("%s: want %d %q, got %s", step, status, want, got)
	}

	expect("no replica", http.StatusServiceUnavailable, "shop is unavailable right now")

	app := runApp("mkdir /www && echo APP > /www/index.html && httpd -f -p 8080 -h /www")
	expect("running", http.StatusOK, "APP")

	set := func(m store.Maintenance) {
		t.Helper()
		if err := c.store.SetServiceMaintenance(ctx, svc.ID, m); err != nil {
			t.Fatal(err)
		}
	}
	set(store.Maintenance{Enabled: true, Title: "Upgrading"})
	expect("maintenance mode", http.StatusServiceUnavailable, "Upgrading")
	set(store.Maintenance{Enabled: true, Title: "Upgrading", AllowIPs: []string{gateway}})
	expect("allowed IP", http.StatusOK, "APP")
	set(store.Maintenance{Enabled: true, Title: "Upgrading", AllowIPs: []string{"192.0.2.1"}})
	expect("other IP", http.StatusServiceUnavailable, "Upgrading")
	set(store.Maintenance{})
	expect("maintenance over", http.StatusOK, "APP")

	if err := dk.RemoveContainer(ctx, app, 0); err != nil {
		t.Fatal(err)
	}
	runApp("sleep 300") // routed, but nothing listens: Traefik's 502
	expect("not listening", http.StatusServiceUnavailable, "shop is unavailable right now")
}

func portOf(t *testing.T, a net.Addr) string {
	t.Helper()
	_, port, err := net.SplitHostPort(a.String())
	if err != nil {
		t.Fatal(err)
	}
	return port
}
