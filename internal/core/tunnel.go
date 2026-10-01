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

// tunnelToken is a server's Cloudflare tunnel token: its own, or for the
// manager's server KIPITINY_CLOUDFLARE_TUNNEL_TOKEN. Empty: public ports.
func (c *Core) tunnelToken(sv store.Server) string {
	if sv.TunnelToken != "" {
		return sv.TunnelToken
	}
	if sv.Kind == store.ServerLocal {
		return c.cfg.Tunnel.Token
	}
	return ""
}

// viaTunnel reports whether a server's public traffic comes through a
// Cloudflare tunnel ("" is the manager's server).
func (c *Core) viaTunnel(ctx context.Context, serverID string) bool {
	if serverID == "" {
		serverID = store.LocalServerID
	}
	sv, err := c.store.GetServer(ctx, serverID)
	if err != nil {
		return serverID == store.LocalServerID && c.cfg.Tunnel.Token != ""
	}
	return c.tunnelToken(sv) != ""
}

// ensureTunnel runs cloudflared on a server with a tunnel token, and removes
// it otherwise. It forwards to https://kipitiny-traefik:443; hostnames are
// routed by the DNS sync (with Cloudflare connected) or in the dashboard.
func (c *Core) ensureTunnel(ctx context.Context, sv store.Server) error {
	dk := c.dockerFor(sv.ID)
	token := c.tunnelToken(sv)
	existing, err := dk.ListContainers(ctx, map[string]string{docker.LabelComponent: tunnelComponent})
	if err != nil {
		return err
	}

	var hash string
	var opts client.ContainerCreateOptions
	if token != "" {
		opts = c.tunnelSpec(token)
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

	if err := dk.EnsureImage(ctx, opts.Config.Image, c.registryAuth(ctx, opts.Config.Image)); err != nil {
		return fmt.Errorf("pull %s: %w", opts.Config.Image, err)
	}
	if _, err := dk.Run(ctx, opts); err != nil {
		return err
	}
	c.log.Info("cloudflare tunnel started", "server", sv.Name, "image", opts.Config.Image)
	return nil
}

func (c *Core) tunnelSpec(token string) client.ContainerCreateOptions {
	return client.ContainerCreateOptions{
		Name: tunnelName,
		Config: &container.Config{
			Image: c.cfg.Tunnel.Image,
			Cmd:   []string{"tunnel", "--no-autoupdate", "run"},
			// The token is read from the environment, keeping it out of `ps`.
			Env: []string{"TUNNEL_TOKEN=" + token},
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
