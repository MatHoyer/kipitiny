package docker

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// ReplaceContainer recreates container id from image, keeping its name,
// configuration, volumes and networks: stop the old one, start the new one,
// and put the old one back if the new one doesn't become healthy. It is how
// the manager upgrades itself, run from a separate updater container.
func (c *Client) ReplaceContainer(ctx context.Context, id, image string, stopTimeout, healthTimeout time.Duration, logf func(string, ...any)) error {
	res, err := c.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return fmt.Errorf("inspect %s: %w", id, err)
	}
	old := res.Container
	spec := replacementSpec(old, image)
	// Values the old image supplied (ENV, HEALTHCHECK, ...) must come from
	// the new image instead, or its changes would be masked.
	if img, err := c.ImageInspect(ctx, old.Image); err != nil {
		logf("inspect previous image, keeping its defaults: %v", err)
	} else if img.Config != nil {
		stripImageDefaults(spec.Config, img.Config)
	}

	logf("stopping %s (running work gets up to %s to finish)", spec.Name, stopTimeout)
	secs := int(stopTimeout.Seconds())
	if _, err := c.ContainerStop(ctx, old.ID, client.ContainerStopOptions{Timeout: &secs}); err != nil {
		return fmt.Errorf("stop: %w", err)
	}
	previous := spec.Name + "-previous"
	if _, err := c.ContainerRename(ctx, old.ID, client.ContainerRenameOptions{NewName: previous}); err != nil {
		c.restart(ctx, old.ID, logf)
		return fmt.Errorf("rename: %w", err)
	}
	rollback := func(newID string, cause error) error {
		logf("update failed, restoring the previous version: %v", cause)
		ctx := context.WithoutCancel(ctx)
		if newID != "" {
			_, _ = c.ContainerRemove(ctx, newID, client.ContainerRemoveOptions{Force: true})
		}
		if _, err := c.ContainerRename(ctx, old.ID, client.ContainerRenameOptions{NewName: spec.Name}); err != nil {
			logf("rename back: %v", err)
		}
		c.restart(ctx, old.ID, logf)
		return cause
	}

	logf("starting %s from %s", spec.Name, image)
	created, err := c.ContainerCreate(ctx, spec)
	if err != nil {
		return rollback("", fmt.Errorf("create: %w", err))
	}
	if _, err := c.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		return rollback(created.ID, fmt.Errorf("start: %w", err))
	}
	if err := c.WaitHealthy(ctx, created.ID, healthTimeout); err != nil {
		return rollback(created.ID, err)
	}
	logf("%s is healthy, removing the previous version", spec.Name)
	if _, err := c.ContainerRemove(ctx, old.ID, client.ContainerRemoveOptions{}); err != nil {
		logf("remove previous container: %v", err)
	}
	return nil
}

func (c *Client) restart(ctx context.Context, id string, logf func(string, ...any)) {
	if _, err := c.ContainerStart(ctx, id, client.ContainerStartOptions{}); err != nil {
		logf("restart previous container: %v", err)
	}
}

// replacementSpec is old's creation spec with a new image.
func replacementSpec(old container.InspectResponse, image string) client.ContainerCreateOptions {
	cfg := *old.Config
	cfg.Image = image
	short := old.ID[:min(12, len(old.ID))]
	if cfg.Hostname == short {
		cfg.Hostname = "" // Docker's default, not a user choice
	}

	host := *old.HostConfig
	host.Mounts = slices.Clone(host.Mounts)
	for _, m := range old.Mounts {
		// Anonymous volumes (e.g. the image's VOLUME /data when none was
		// mounted there) would otherwise start out empty.
		if m.Type == mount.TypeVolume && !mounted(host, m.Destination) {
			host.Mounts = append(host.Mounts, mount.Mount{Type: mount.TypeVolume, Source: m.Name, Target: m.Destination})
		}
	}

	endpoints := map[string]*network.EndpointSettings{}
	if old.NetworkSettings != nil {
		for name, ep := range old.NetworkSettings.Networks {
			if ep == nil {
				continue
			}
			endpoints[name] = &network.EndpointSettings{
				IPAMConfig: ep.IPAMConfig,
				Links:      ep.Links,
				Aliases:    slices.DeleteFunc(slices.Clone(ep.Aliases), func(a string) bool { return a == short }),
				DriverOpts: ep.DriverOpts,
			}
		}
	}
	return client.ContainerCreateOptions{
		Name:             strings.TrimPrefix(old.Name, "/"),
		Config:           &cfg,
		HostConfig:       &host,
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: endpoints},
	}
}

// stripImageDefaults removes from cfg what it inherited from img, so the
// replacement inherits from its own image. User-set values are kept.
func stripImageDefaults(cfg *container.Config, img *dockerspec.DockerOCIImageConfig) {
	cfg.Env = slices.DeleteFunc(cfg.Env, func(e string) bool { return slices.Contains(img.Env, e) })
	for k, v := range cfg.Labels {
		if iv, ok := img.Labels[k]; ok && iv == v {
			delete(cfg.Labels, k)
		}
	}
	for p := range cfg.ExposedPorts {
		if _, ok := img.ExposedPorts[p.String()]; ok {
			delete(cfg.ExposedPorts, p)
		}
	}
	for v := range cfg.Volumes {
		if _, ok := img.Volumes[v]; ok {
			delete(cfg.Volumes, v)
		}
	}
	if slices.Equal(cfg.Entrypoint, img.Entrypoint) {
		cfg.Entrypoint = nil
	}
	if slices.Equal(cfg.Cmd, img.Cmd) {
		cfg.Cmd = nil
	}
	if cfg.WorkingDir == img.WorkingDir {
		cfg.WorkingDir = ""
	}
	if cfg.User == img.User {
		cfg.User = ""
	}
	if cfg.StopSignal == img.StopSignal {
		cfg.StopSignal = ""
	}
	if reflect.DeepEqual(cfg.Healthcheck, img.Healthcheck) {
		cfg.Healthcheck = nil
	}
}

// mounted reports whether host already mounts something at target.
func mounted(host container.HostConfig, target string) bool {
	for _, b := range host.Binds {
		parts := strings.Split(b, ":")
		if len(parts) >= 2 && parts[1] == target {
			return true
		}
	}
	return slices.ContainsFunc(host.Mounts, func(m mount.Mount) bool { return m.Target == target })
}
