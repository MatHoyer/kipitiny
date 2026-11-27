package docker

import (
	"encoding/base64"
	"encoding/json"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/registry"
	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

// A spec becomes the create request the use cases built by hand before.
func TestCreateOptions(t *testing.T) {
	stop, init := 30, true
	spec := domain.ContainerSpec{
		Name: "p-web-1", Image: "nginx", Platform: &domain.Platform{OS: "linux", Architecture: "arm64"},
		Cmd: []string{"run"}, Entrypoint: []string{"/init"}, Env: []string{"A=1"}, User: "1000", WorkingDir: "/app",
		Labels:       map[string]string{domain.LabelManaged: "true"},
		Healthcheck:  &domain.HealthConfig{Test: []string{"CMD", "true"}, Interval: time.Second, Retries: 3},
		ExposedPorts: []string{"53/udp"}, StopSignal: "SIGINT", StopTimeout: &stop, Tty: true, OpenStdin: true,
		NetworkMode: "host", Networks: map[string][]string{"kipitiny-p": {"web"}, domain.ProxyNetwork: nil},
		Mounts: []domain.Mount{
			{Type: domain.MountVolume, Source: "data", Target: "/data", Volume: &domain.VolumeOptions{
				Labels: map[string]string{"a": "b"}, Driver: &domain.VolumeDriver{Name: "local", Options: map[string]string{"type": "nfs"}}}},
			{Type: domain.MountBind, Source: "/run/docker.sock", Target: "/var/run/docker.sock", ReadOnly: true},
		},
		PortBindings: map[string][]domain.PortBinding{"53/udp": {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: "5353"}}},
		Restart:      domain.RestartPolicy{Name: "on-failure", MaxRetries: 2}, Log: true,
		Memory: 1 << 20, MemoryReservation: 1 << 19, NanoCPUs: 5e8, ShmSize: 1 << 26, Tmpfs: map[string]string{"/tmp": "size=1m"},
		ReadonlyRootfs: true, Init: &init, Privileged: true, PidMode: "host", CapAdd: []string{"NET_ADMIN"}, CapDrop: []string{"NET_RAW"},
		SecurityOpt: []string{"no-new-privileges"}, ExtraHosts: []string{"h:host-gateway"}, Binds: []string{"/a:/b"},
		Sysctls: map[string]string{"net.core.somaxconn": "1024"}, Ulimits: []domain.ContainerUlimit{{Name: "nofile", Soft: 1, Hard: 2}},
		Devices: []domain.DeviceMapping{{PathOnHost: "/dev/fuse", PathInContainer: "/dev/fuse", CgroupPermissions: "rwm"}},
	}
	udp := network.MustParsePort("53/udp")
	want := client.ContainerCreateOptions{
		Name: "p-web-1",
		Config: &container.Config{
			Image: "nginx", Cmd: []string{"run"}, Entrypoint: []string{"/init"}, Env: []string{"A=1"}, User: "1000", WorkingDir: "/app",
			Labels:       map[string]string{domain.LabelManaged: "true"},
			Healthcheck:  &container.HealthConfig{Test: []string{"CMD", "true"}, Interval: time.Second, Retries: 3},
			ExposedPorts: network.PortSet{udp: {}}, StopSignal: "SIGINT", StopTimeout: &stop, Tty: true, OpenStdin: true,
		},
		HostConfig: &container.HostConfig{
			NetworkMode: "host",
			Mounts: []mount.Mount{
				{Type: mount.TypeVolume, Source: "data", Target: "/data", VolumeOptions: &mount.VolumeOptions{
					Labels: map[string]string{"a": "b"}, DriverConfig: &mount.Driver{Name: "local", Options: map[string]string{"type": "nfs"}}}},
				{Type: mount.TypeBind, Source: "/run/docker.sock", Target: "/var/run/docker.sock", ReadOnly: true},
			},
			PortBindings:  network.PortMap{udp: {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: "5353"}}},
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyOnFailure, MaximumRetryCount: 2}, LogConfig: DefaultLogConfig(),
			Resources: container.Resources{
				Memory: 1 << 20, MemoryReservation: 1 << 19, NanoCPUs: 5e8, Ulimits: []*container.Ulimit{{Name: "nofile", Soft: 1, Hard: 2}},
				Devices: []container.DeviceMapping{{PathOnHost: "/dev/fuse", PathInContainer: "/dev/fuse", CgroupPermissions: "rwm"}},
			},
			ShmSize: 1 << 26, Tmpfs: map[string]string{"/tmp": "size=1m"}, ReadonlyRootfs: true, Init: &init, Privileged: true, PidMode: "host",
			CapAdd: []string{"NET_ADMIN"}, CapDrop: []string{"NET_RAW"}, SecurityOpt: []string{"no-new-privileges"},
			ExtraHosts: []string{"h:host-gateway"}, Binds: []string{"/a:/b"}, Sysctls: map[string]string{"net.core.somaxconn": "1024"},
		},
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			"kipitiny-p": {Aliases: []string{"web"}}, domain.ProxyNetwork: {},
		}},
		Platform: &ocispec.Platform{OS: "linux", Architecture: "arm64"},
	}
	if got := createOptions(spec); !reflect.DeepEqual(got, want) {
		t.Errorf("createOptions:\n got %+v %+v %+v\nwant %+v %+v %+v", got.Config, got.HostConfig, got.NetworkingConfig, want.Config, want.HostConfig, want.NetworkingConfig)
	}

	// The zero spec sets nothing: no networking config, Docker's logging.
	bare := createOptions(domain.ContainerSpec{Image: "busybox"})
	if bare.NetworkingConfig != nil || bare.Platform != nil || bare.HostConfig.LogConfig.Type != "" || bare.Config.ExposedPorts != nil || bare.HostConfig.PortBindings != nil {
		t.Errorf("bare spec: %+v", bare)
	}
}

func TestToContainer(t *testing.T) {
	ip := netip.MustParseAddr("172.18.0.2")
	got := toContainer(container.Summary{
		ID: "abc", Names: []string{"/web"}, Image: "nginx", ImageID: "sha256:1", Labels: map[string]string{"a": "b"},
		State: container.StateRunning, Status: "Up", Health: &container.HealthSummary{Status: container.Healthy},
		NetworkSettings: &container.NetworkSettingsSummary{Networks: map[string]*network.EndpointSettings{"proxy": {IPAddress: ip, Aliases: []string{"web"}}, "gone": nil}},
	})
	want := domain.Container{
		ID: "abc", Names: []string{"/web"}, Image: "nginx", ImageID: "sha256:1", Labels: map[string]string{"a": "b"},
		State: domain.StateRunning, Status: "Up", Health: domain.Healthy, Networks: map[string]domain.ContainerEndpoint{"proxy": {IP: ip, Aliases: []string{"web"}}, "gone": {}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("toContainer = %+v", got)
	}
	if got := toContainer(container.Summary{ID: "x"}); got.Health != "" || got.Networks != nil {
		t.Errorf("bare summary = %+v", got)
	}
	// Network settings without networks: attached to none, not unknown.
	if got := toContainer(container.Summary{NetworkSettings: &container.NetworkSettingsSummary{}}); got.Networks == nil {
		t.Error("no networks: nil")
	}

	info := toContainerInfo(container.InspectResponse{
		ID: "abc", Name: "/web", RestartCount: 2, Config: &container.Config{Image: "nginx", Labels: map[string]string{"a": "b"}},
		State: &container.State{Status: container.StateExited, ExitCode: 137, OOMKilled: true, Health: &container.Health{Status: container.Unhealthy}},
	})
	wantInfo := domain.ContainerInfo{ID: "abc", Name: "/web", Image: "nginx", Labels: map[string]string{"a": "b"}, RestartCount: 2,
		State: &domain.ContainerState{Status: domain.StateExited, ExitCode: 137, OOMKilled: true, Health: domain.Unhealthy}}
	if !reflect.DeepEqual(info, wantInfo) {
		t.Errorf("toContainerInfo = %+v %+v", info, info.State)
	}
	if info := toContainerInfo(container.InspectResponse{ID: "x"}); info.State != nil {
		t.Errorf("no state = %+v", info.State)
	}
}

func TestFilters(t *testing.T) {
	f := filters(domain.Filter{}.Add("label", "a=1", "b").Add("until", "24h"))
	want := make(client.Filters).Add("label", "a=1", "b").Add("until", "24h")
	if !reflect.DeepEqual(f, want) {
		t.Errorf("filters = %v", f)
	}
	if got := domain.LabelFilter(map[string]string{"a": "1"}); !reflect.DeepEqual(got, domain.Filter{"label": {"a=1"}}) {
		t.Errorf("LabelFilter = %v", got)
	}
}

// domain.RegistryAuth encodes a credential as the client did with moby's own type.
func TestRegistryAuth(t *testing.T) {
	b, _ := json.Marshal(registry.AuthConfig{Username: "me", Password: "secret", ServerAddress: "ghcr.io"})
	if got, want := domain.RegistryAuth("ghcr.io", "me", "secret"), base64.URLEncoding.EncodeToString(b); got != want {
		t.Errorf("RegistryAuth = %s, want %s", got, want)
	}
}
