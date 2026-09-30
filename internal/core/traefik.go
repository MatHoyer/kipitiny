package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	traefikName       = "kipitiny-traefik"
	traefikComponent  = "traefik"
	traefikACMEVolume = "kipitiny-traefik-acme"
	certResolver      = "letsencrypt"
)

// ensureTraefik makes sure exactly one Traefik container runs with the
// current configuration, recreating it when the configuration changed.
func (c *Core) ensureTraefik(ctx context.Context) error {
	cfg := c.cfg.Traefik
	opts := c.traefikSpec()
	hash := specHash(cfg.Image, opts.Config.Cmd, cfg.HTTPPort, cfg.HTTPSPort, cfg.DockerSocket)
	opts.Config.Labels[docker.LabelConfigHash] = hash

	existing, err := c.docker.ListContainers(ctx, map[string]string{docker.LabelComponent: traefikComponent})
	if err != nil {
		return err
	}
	if len(existing) == 1 && existing[0].Labels[docker.LabelConfigHash] == hash {
		if existing[0].State == container.StateRunning {
			return nil
		}
		_, err := c.docker.ContainerStart(ctx, existing[0].ID, client.ContainerStartOptions{})
		return err
	}
	for _, ct := range existing {
		c.log.Info("removing outdated traefik container", "id", ct.ID[:12])
		if err := c.docker.RemoveContainer(ctx, ct.ID, 10*time.Second); err != nil {
			return err
		}
	}

	if err := c.docker.EnsureImage(ctx, cfg.Image); err != nil {
		return fmt.Errorf("pull %s: %w", cfg.Image, err)
	}
	if _, err := c.docker.Run(ctx, opts); err != nil {
		return err
	}
	c.log.Info("traefik started", "image", cfg.Image, "http", cfg.HTTPPort, "https", cfg.HTTPSPort)
	return nil
}

func (c *Core) traefikSpec() client.ContainerCreateOptions {
	cfg := c.cfg.Traefik

	// Redirect to the public HTTPS port, which differs from 443 in dev setups.
	redirectTo := "websecure"
	if cfg.HTTPSPort != "443" {
		redirectTo = ":" + cfg.HTTPSPort
	}
	args := []string{
		"--global.checknewversion=false",
		"--global.sendanonymoususage=false",
		"--log.level=INFO",
		"--providers.docker=true",
		"--providers.docker.exposedbydefault=false",
		"--providers.docker.network=" + docker.ProxyNetwork,
		// Ignore containers the manager does not own.
		"--providers.docker.constraints=Label(`" + docker.LabelManaged + "`,`true`)",
		"--entrypoints.web.address=:80",
		"--entrypoints.web.http.redirections.entrypoint.to=" + redirectTo,
		"--entrypoints.web.http.redirections.entrypoint.scheme=https",
		"--entrypoints.websecure.address=:443",
		"--certificatesresolvers." + certResolver + ".acme.tlschallenge=true",
		"--certificatesresolvers." + certResolver + ".acme.storage=/acme/acme.json",
	}
	if cfg.ACMEEmail != "" {
		args = append(args, "--certificatesresolvers."+certResolver+".acme.email="+cfg.ACMEEmail)
	}

	http, https := network.MustParsePort("80/tcp"), network.MustParsePort("443/tcp")
	return client.ContainerCreateOptions{
		Name: traefikName,
		Config: &container.Config{
			Image:        cfg.Image,
			Cmd:          args,
			ExposedPorts: network.PortSet{http: {}, https: {}},
			Labels: map[string]string{
				docker.LabelManaged:   "true",
				docker.LabelComponent: traefikComponent,
			},
		},
		HostConfig: &container.HostConfig{
			PortBindings: network.PortMap{
				http:  {{HostPort: cfg.HTTPPort}},
				https: {{HostPort: cfg.HTTPSPort}},
			},
			Mounts: []mount.Mount{
				{Type: mount.TypeBind, Source: cfg.DockerSocket, Target: "/var/run/docker.sock", ReadOnly: true},
				{Type: mount.TypeVolume, Source: traefikACMEVolume, Target: "/acme"},
			},
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			LogConfig:     docker.DefaultLogConfig(),
		},
		NetworkingConfig: &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{docker.ProxyNetwork: {}},
		},
	}
}

// traefikLabels routes HTTPS traffic for svc.Domain to svc.Port.
func traefikLabels(svc store.Service) map[string]string {
	name := "kipitiny-" + strings.ToLower(svc.ID)
	return map[string]string{
		"traefik.enable":                                              "true",
		"traefik.docker.network":                                      docker.ProxyNetwork,
		"traefik.http.routers." + name + ".rule":                      "Host(`" + svc.Domain + "`)",
		"traefik.http.routers." + name + ".entrypoints":               "websecure",
		"traefik.http.routers." + name + ".tls.certresolver":          certResolver,
		"traefik.http.routers." + name + ".service":                   name,
		"traefik.http.services." + name + ".loadbalancer.server.port": fmt.Sprint(svc.Port),
	}
}

func specHash(parts ...any) string {
	b, _ := json.Marshal(parts)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}
