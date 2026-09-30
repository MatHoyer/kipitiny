package core

import (
	"context"
	"fmt"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	tunnelName      = "kipitiny-cloudflared"
	tunnelComponent = "cloudflared"
)

// ensureTunnel runs cloudflared on the manager's own server when a tunnel
// token is configured, and removes it otherwise. Public hostnames are set in
// the Cloudflare dashboard, pointing at https://kipitiny-traefik:443.
func (c *Core) ensureTunnel(ctx context.Context, sv store.Server) error {
	if sv.Kind != store.ServerLocal {
		return nil
	}
	dk := c.dockerFor(sv.ID)
	existing, err := dk.ListContainers(ctx, map[string]string{docker.LabelComponent: tunnelComponent})
	if err != nil {
		return err
	}

	var hash string
	var opts client.ContainerCreateOptions
	if c.cfg.Tunnel.Token != "" {
		opts = c.tunnelSpec()
		hash = specHash(opts.Config.Image, opts.Config.Cmd, opts.Config.Env)
		opts.Config.Labels[docker.LabelConfigHash] = hash
		if len(existing) == 1 && existing[0].Labels[docker.LabelConfigHash] == hash {
			if existing[0].State == container.StateRunning {
				return nil
			}
			_, err := dk.ContainerStart(ctx, existing[0].ID, client.ContainerStartOptions{})
			return err
		}
	}
	for _, ct := range existing {
		c.log.Info("removing outdated cloudflared container", "id", ct.ID[:12])
		if err := dk.RemoveContainer(ctx, ct.ID, 10*time.Second); err != nil {
			return err
		}
	}
	if hash == "" {
		return nil
	}

	if err := dk.EnsureImage(ctx, opts.Config.Image); err != nil {
		return fmt.Errorf("pull %s: %w", opts.Config.Image, err)
	}
	if _, err := dk.Run(ctx, opts); err != nil {
		return err
	}
	c.log.Info("cloudflare tunnel started", "image", opts.Config.Image)
	return nil
}

func (c *Core) tunnelSpec() client.ContainerCreateOptions {
	return client.ContainerCreateOptions{
		Name: tunnelName,
		Config: &container.Config{
			Image: c.cfg.Tunnel.Image,
			Cmd:   []string{"tunnel", "--no-autoupdate", "run"},
			// The token is read from the environment, keeping it out of `ps`.
			Env: []string{"TUNNEL_TOKEN=" + c.cfg.Tunnel.Token},
			Labels: map[string]string{
				docker.LabelManaged:   "true",
				docker.LabelComponent: tunnelComponent,
			},
		},
		HostConfig: &container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			LogConfig:     docker.DefaultLogConfig(),
		},
		NetworkingConfig: &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{docker.ProxyNetwork: {}},
		},
	}
}
