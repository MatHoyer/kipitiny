package docker

import (
	"slices"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestReplacementSpec(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	old := container.InspectResponse{
		ID:   id,
		Name: "/kipitiny-manager-1",
		Config: &container.Config{
			Image:    "ghcr.io/mathoyer/kipitiny:1.0.0",
			Hostname: id[:12],
			Env:      []string{"KIPITINY_DOMAIN=k.example.com"},
		},
		HostConfig: &container.HostConfig{
			Binds:  []string{"/var/run/docker.sock:/var/run/docker.sock"},
			Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: "manager-data", Target: "/data"}},
		},
		Mounts: []container.MountPoint{
			{Type: mount.TypeVolume, Name: "manager-data", Destination: "/data"},
			{Type: mount.TypeVolume, Name: "3f2a", Destination: "/cache"}, // anonymous
			{Type: mount.TypeBind, Source: "/var/run/docker.sock", Destination: "/var/run/docker.sock"},
		},
		NetworkSettings: &container.NetworkSettings{Networks: map[string]*network.EndpointSettings{
			"kipitiny_default": {Aliases: []string{"manager", id[:12]}},
			"kipitiny-proxy":   {Aliases: []string{"kipitiny-manager"}},
		}},
	}

	spec := replacementSpec(old, "ghcr.io/mathoyer/kipitiny:1.1.0")
	if spec.Name != "kipitiny-manager-1" || spec.Config.Image != "ghcr.io/mathoyer/kipitiny:1.1.0" {
		t.Errorf("name/image = %q %q", spec.Name, spec.Config.Image)
	}
	if spec.Config.Hostname != "" {
		t.Error("the default hostname (old ID) must not be carried over")
	}
	if old.Config.Image != "ghcr.io/mathoyer/kipitiny:1.0.0" {
		t.Error("old config mutated")
	}
	want := []mount.Mount{
		{Type: mount.TypeVolume, Source: "manager-data", Target: "/data"},
		{Type: mount.TypeVolume, Source: "3f2a", Target: "/cache"},
	}
	if !slices.Equal(spec.HostConfig.Mounts, want) {
		t.Errorf("mounts = %+v", spec.HostConfig.Mounts)
	}
	eps := spec.NetworkingConfig.EndpointsConfig
	if !slices.Equal(eps["kipitiny_default"].Aliases, []string{"manager"}) || !slices.Equal(eps["kipitiny-proxy"].Aliases, []string{"kipitiny-manager"}) {
		t.Errorf("endpoints = %+v %+v", eps["kipitiny_default"], eps["kipitiny-proxy"])
	}
}

func TestStripImageDefaults(t *testing.T) {
	hc := &container.HealthConfig{Test: []string{"CMD", "/kipitiny", "healthcheck"}}
	cfg := &container.Config{
		Env:         []string{"PATH=/bin", "KIPITINY_DATA_DIR=/data", "KIPITINY_DOMAIN=k.example.com"},
		Labels:      map[string]string{"org.opencontainers.image.version": "1.0.0", "com.docker.compose.service": "manager"},
		Entrypoint:  []string{"/kipitiny"},
		Healthcheck: hc,
		Volumes:     map[string]struct{}{"/data": {}},
	}
	img := &dockerspec.DockerOCIImageConfig{
		ImageConfig: ocispec.ImageConfig{
			Env:        []string{"PATH=/bin", "KIPITINY_DATA_DIR=/data"},
			Labels:     map[string]string{"org.opencontainers.image.version": "1.0.0"},
			Entrypoint: []string{"/kipitiny"},
			Volumes:    map[string]struct{}{"/data": {}},
		},
		DockerOCIImageConfigExt: dockerspec.DockerOCIImageConfigExt{Healthcheck: &container.HealthConfig{Test: []string{"CMD", "/kipitiny", "healthcheck"}}},
	}
	stripImageDefaults(cfg, img)
	if !slices.Equal(cfg.Env, []string{"KIPITINY_DOMAIN=k.example.com"}) {
		t.Errorf("env = %v", cfg.Env)
	}
	if len(cfg.Labels) != 1 || cfg.Labels["com.docker.compose.service"] != "manager" {
		t.Errorf("labels = %v", cfg.Labels)
	}
	if cfg.Entrypoint != nil || cfg.Healthcheck != nil || len(cfg.Volumes) != 0 {
		t.Errorf("image defaults kept: %+v", cfg)
	}
}
