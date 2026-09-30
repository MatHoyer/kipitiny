package core

import (
	"context"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// probeDir is where the manager's binary is mounted in app containers.
const probeDir = "/.kipitiny"

// probeMount exposes the manager's static binary to app containers, read-only:
// its own image when the manager runs in Docker, the executable otherwise.
func (c *Core) probeMount(ctx context.Context) (mount.Mount, error) {
	if self := c.selfContainer(ctx); self != "" {
		res, err := c.docker.ContainerInspect(ctx, self, client.ContainerInspectOptions{})
		if err != nil {
			return mount.Mount{}, err
		}
		return mount.Mount{Type: mount.TypeImage, Source: res.Container.Image, Target: probeDir, ReadOnly: true}, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return mount.Mount{}, err
	}
	return mount.Mount{Type: mount.TypeBind, Source: exe, Target: probeDir + "/kipitiny", ReadOnly: true}, nil
}

// imageHealthcheck reports whether an image defines its own HEALTHCHECK.
func (c *Core) imageHealthcheck(ctx context.Context, image string) (bool, error) {
	res, err := c.docker.ImageInspect(ctx, image)
	if err != nil {
		return false, err
	}
	cfg := res.Config
	return cfg != nil && cfg.Healthcheck != nil && len(cfg.Healthcheck.Test) > 0 &&
		!slices.Equal(cfg.Healthcheck.Test, []string{"NONE"}), nil
}

// withProbe gives an app replica a healthcheck: an HTTP GET of the health
// path, or a TCP connect to the port. Traefik only routes to healthy
// containers, so a new replica gets traffic once it actually serves, and one
// that stops answering is taken out of rotation.
func withProbe(spec *client.ContainerCreateOptions, svc store.Service, m mount.Mount) {
	target := fmt.Sprintf("tcp://127.0.0.1:%d", svc.Port)
	if svc.HealthPath != "" {
		target = fmt.Sprintf("http://127.0.0.1:%d%s", svc.Port, svc.HealthPath)
	}
	spec.Config.Healthcheck = &container.HealthConfig{
		Test:     []string{"CMD", probeDir + "/kipitiny", "probe", target},
		Interval: 10 * time.Second,
		Timeout:  5 * time.Second,
		Retries:  3,
		// Probe every second while starting, so readiness is noticed fast.
		StartPeriod:   readyTimeout,
		StartInterval: time.Second,
	}
	spec.HostConfig.Mounts = append(spec.HostConfig.Mounts, m)
}
