package core

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestComposeRuntime(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	p, err := c.store.CreateProject(ctx, store.Project{Name: "shop", ServerID: store.LocalServerID})
	if err != nil {
		t.Fatal(err)
	}
	src := `
services:
  worker:
    image: ghcr.io/me/shop:1
    command: sh -c 'echo $$HOME && exec worker'
    entrypoint: ""
    user: "1000"
    working_dir: /app
    labels: {com.example.team: shop, kipitiny.service: evil}
    tmpfs: [/tmp:size=64m]
    shm_size: 1g
    ulimits: {nofile: {soft: 1024, hard: 4096}}
    sysctls: {net.core.somaxconn: "1024"}
    read_only: true
    init: true
    stop_signal: SIGINT
    extra_hosts: ["host.docker.internal:host-gateway"]
`
	if _, err := c.ApplyCompose(ctx, p.ID, []byte(src), ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	svcs, _ := c.store.ListServices(ctx, p.ID)
	w := svcs[0]
	if !slices.Equal(w.Runtime.Command, []string{"sh", "-c", "echo $HOME && exec worker"}) {
		t.Errorf("command = %q", w.Runtime.Command)
	}

	spec := containerSpec(p, w, envSources{}, "D1", 1, route{})
	cfg, host := spec.Config, spec.HostConfig
	if !slices.Equal(cfg.Entrypoint, []string{""}) || cfg.User != "1000" || cfg.WorkingDir != "/app" || cfg.StopSignal != "SIGINT" {
		t.Errorf("config = %+v", cfg)
	}
	if cfg.Labels["com.example.team"] != "shop" || cfg.Labels[docker.LabelService] != w.ID {
		t.Errorf("labels = %v", cfg.Labels)
	}
	if host.Tmpfs["/tmp"] != "size=64m" || host.ShmSize != 1<<30 || !host.ReadonlyRootfs || host.Init == nil || !*host.Init ||
		len(host.Ulimits) != 1 || host.Ulimits[0].Hard != 4096 || host.Sysctls["net.core.somaxconn"] != "1024" ||
		!slices.Equal(host.ExtraHosts, []string{"host.docker.internal:host-gateway"}) {
		t.Errorf("host config = %+v", host)
	}

	out, err := c.ExportCompose(ctx, p.ID, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := c.ApplyCompose(ctx, p.ID, out.Compose, ApplyOptions{DryRun: true})
	if err != nil || !slices.Equal(plan.Unchanged, []string{"worker"}) {
		t.Errorf("own export re-applied: %+v, %v\n%s", plan, err, out.Compose)
	}
	plan, err = c.ApplyCompose(ctx, p.ID, []byte("services: {worker: {image: \"ghcr.io/me/shop:1\"}}"), ApplyOptions{DryRun: true})
	if err != nil || len(plan.Update) != 1 || !slices.Equal(plan.Update[0].Fields, []string{"runtime"}) {
		t.Errorf("dropping the runtime keys: %+v, %v", plan, err)
	}
}

func TestValidateRuntime(t *testing.T) {
	app := store.Service{Kind: store.ServiceKindApp, Image: "x", Replicas: 1}
	for name, r := range map[string]store.Runtime{
		"relative workdir":  {WorkingDir: "app"},
		"traefik label":     {Labels: map[string]string{"traefik.http.routers.x.rule": "Host(`a`)"}},
		"relative tmpfs":    {Tmpfs: map[string]string{"tmp": ""}},
		"unknown ulimit":    {Ulimits: map[string]store.Ulimit{"files": {Soft: 1, Hard: 1}}},
		"soft above hard":   {Ulimits: map[string]store.Ulimit{"nofile": {Soft: 2, Hard: 1}}},
		"bad sysctl":        {Sysctls: map[string]string{"nope": "1"}},
		"bad signal":        {StopSignal: "sigint now"},
		"bad extra host":    {ExtraHosts: []string{"db:not-an-ip"}},
		"negative shm size": {ShmSizeMB: -1},
	} {
		s := app
		s.Runtime = r
		if err := validateService(s); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	s := app
	s.Runtime = store.Runtime{Command: []string{"run"}, ExtraHosts: []string{"db:10.0.0.2", "v6:::1"}, StopSignal: "SIGTERM",
		Ulimits: map[string]store.Ulimit{"nofile": {Soft: -1, Hard: -1}}}
	if err := validateService(s); err != nil {
		t.Errorf("valid: %v", err)
	}
	db := redisService()
	db.Runtime = store.Runtime{User: "root"}
	if err := validateService(db); !errors.Is(err, ErrInvalid) {
		t.Errorf("database: got %v", err)
	}
}

func TestRuntimeHostAccess(t *testing.T) {
	app := store.Service{ID: "S1", ProjectID: "P1", Kind: store.ServiceKindApp, Image: "x", Replicas: 1, Env: map[string]string{}}
	app.Runtime = normalizeRuntime(store.Runtime{CapAdd: []string{"cap_net_admin"}, CapDrop: []string{"MKNOD"},
		Devices: []string{"/dev/net/tun", "/dev/ttyUSB0:/dev/ttyACM0:r"}, SecurityOpt: []string{"apparmor=unconfined"}})
	if err := validateService(app); err != nil {
		t.Fatal(err)
	}
	host := containerSpec(store.Project{ID: "P1", Name: "vpn"}, app, envSources{}, "D1", 1, route{}).HostConfig
	if !slices.Equal(host.CapAdd, []string{"NET_ADMIN"}) || !slices.Equal(host.CapDrop, []string{"NET_RAW", "MKNOD"}) ||
		!slices.Equal(host.SecurityOpt, []string{"apparmor=unconfined"}) || len(host.Devices) != 2 ||
		host.Devices[0].PathInContainer != "/dev/net/tun" || host.Devices[0].CgroupPermissions != "rwm" ||
		host.Devices[1].PathInContainer != "/dev/ttyACM0" || host.Devices[1].CgroupPermissions != "r" {
		t.Errorf("host config = %+v", host)
	}
	raw := app
	raw.Runtime = store.Runtime{CapAdd: []string{"NET_RAW"}}
	if host := containerSpec(store.Project{ID: "P1"}, raw, envSources{}, "D1", 1, route{}).HostConfig; slices.Contains(host.CapDrop, "NET_RAW") {
		t.Errorf("NET_RAW added back is still dropped: %v", host.CapDrop)
	}
	for _, r := range []store.Runtime{{CapAdd: []string{"net admin"}}, {Devices: []string{"dev/x"}}, {Devices: []string{"/dev/x:/dev/x:rwx"}}} {
		bad := app
		bad.Runtime = r
		if err := validateService(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: got %v", r, err)
		}
	}

	member := WithActor(context.Background(), Actor{Kind: "user", Name: "alice", Role: store.RoleMember})
	admin := WithActor(context.Background(), Actor{Kind: "user", Name: "root", Role: store.RoleAdmin})
	plain := app
	plain.Runtime = store.Runtime{}
	if err := checkServiceChange(member, plain, app); !errors.Is(err, ErrForbidden) {
		t.Errorf("member adding host access: %v", err)
	}
	if err := checkServiceChange(admin, plain, app); err != nil {
		t.Errorf("admin: %v", err)
	}
	if err := checkServiceChange(member, app, plain); errors.Is(err, ErrForbidden) {
		t.Errorf("member removing host access: %v", err)
	}
	cmd := app
	cmd.Runtime.Command = []string{"run"}
	if err := checkServiceChange(member, app, cmd); errors.Is(err, ErrForbidden) {
		t.Errorf("member changing the command of an app with host access: %v", err)
	}
}

func TestPortsAndVolumesHostAccess(t *testing.T) {
	app := store.Service{ID: "S1", ProjectID: "P1", Kind: store.ServiceKindApp, Image: "x", Replicas: 1, Env: map[string]string{},
		PublishedPorts: normalizePorts([]store.PublishedPort{{HostIP: "127.0.0.1", HostPort: 5432}, {HostIP: "0.0.0.0", HostPort: 80},
			{HostIP: "10.0.0.1", HostPort: 5432}}),
		Volumes: normalizeVolumes([]store.Volume{{Name: "conf", Path: "/conf", ReadOnly: true}, {Name: "media", Path: "/media", External: "nas"},
			{Name: "nfs", Path: "/nfs", DriverOpts: map[string]string{"type": "nfs", "o": "addr=10.0.0.5", "device": ":/x"}}})}
	if err := validateService(app); err != nil {
		t.Fatal(err)
	}
	if app.PublishedPorts[1].HostIP != "" {
		t.Errorf("0.0.0.0 is every address: %+v", app.PublishedPorts[1])
	}
	host := containerSpec(store.Project{ID: "P1"}, app, envSources{}, "D1", 1, route{}).HostConfig
	var ips []string
	for _, bs := range host.PortBindings {
		for _, b := range bs {
			ips = append(ips, b.HostIP.String()+":"+b.HostPort)
		}
	}
	slices.Sort(ips)
	if !slices.Equal(ips, []string{"10.0.0.1:5432", "127.0.0.1:5432", "invalid IP:80"}) {
		t.Errorf("bindings = %v", ips)
	}
	m := host.Mounts
	if len(m) != 3 || !m[0].ReadOnly || m[1].Source != "nas" || m[1].VolumeOptions != nil ||
		m[2].VolumeOptions.DriverConfig == nil || m[2].VolumeOptions.DriverConfig.Options["type"] != "nfs" {
		t.Errorf("mounts = %+v", m)
	}

	clash := app
	clash.PublishedPorts = append(slices.Clone(app.PublishedPorts), store.PublishedPort{HostPort: 5432, ContainerPort: 1, Protocol: "tcp"})
	if err := validateService(clash); !errors.Is(err, ErrInvalid) {
		t.Errorf("every address clashes with 127.0.0.1: %v", err)
	}
	for _, v := range []store.Volume{{Name: "a", Path: "/a", External: "bad name"}, {Name: "a", Path: "/a", DriverOpts: map[string]string{"uid": "1"}}} {
		bad := app
		bad.Volumes = []store.Volume{v}
		if err := validateService(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: got %v", v, err)
		}
	}

	member := WithActor(context.Background(), Actor{Kind: "user", Name: "alice", Role: store.RoleMember})
	plain := app
	plain.Volumes = app.Volumes[:1]
	if err := checkServiceChange(member, plain, app); !errors.Is(err, ErrForbidden) {
		t.Errorf("member adding an external volume: %v", err)
	}
	ro := app
	ro.Volumes = slices.Clone(app.Volumes)
	ro.Volumes[1].ReadOnly = true
	if err := checkServiceChange(member, app, ro); errors.Is(err, ErrForbidden) {
		t.Errorf("member changing an external volume's mount: %v", err)
	}
	if err := checkServiceChange(member, ro, app); !errors.Is(err, ErrForbidden) {
		t.Errorf("member making a read-only external volume writable: %v", err)
	}
	admin := WithActor(context.Background(), Actor{Kind: "user", Name: "root", Role: store.RoleAdmin})
	if err := checkServiceChange(admin, ro, app); err != nil {
		t.Errorf("admin making a read-only external volume writable: %v", err)
	}
	conf := app
	conf.Volumes = slices.Clone(app.Volumes)
	conf.Volumes[0].ReadOnly = false
	if err := checkServiceChange(member, app, conf); err != nil {
		t.Errorf("member making their own volume writable: %v", err)
	}
}
