package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	traefikName       = "kipitiny-traefik"
	traefikComponent  = "traefik"
	traefikACMEVolume = "kipitiny-traefik-acme"
	certResolver      = "letsencrypt"

	// managerAlias names the manager on the proxy network when it runs in a
	// container; otherwise Traefik reaches it on the host.
	managerAlias     = "kipitiny-manager"
	managerHost      = "host.docker.internal"
	managerRouteFile = "/etc/traefik/kipitiny-manager.yml"
)

// ensureTraefik makes sure exactly one Traefik container runs with the
// current configuration, recreating it when the configuration changed.
func (c *Core) ensureTraefik(ctx context.Context, sv store.Server) error {
	cfg := c.cfg.Traefik
	socket := socketPath(sv, c.cfg)
	dk := c.dockerFor(sv.ID)
	var managerURL string
	if c.cfg.Domain != "" && sv.Kind == store.ServerLocal {
		u, err := c.managerUpstream(ctx, dk)
		if err != nil {
			return err
		}
		managerURL = u
	}
	opts, files := c.traefikSpec(socket, managerURL, c.viaTunnel(sv.ID))
	// Keep this formula stable: any change recreates Traefik on every server
	// at upgrade, a short outage for all apps. Tunnel mode changes Cmd.
	hash := specHash(cfg.Image, opts.Config.Cmd, cfg.HTTPPort, cfg.HTTPSPort, socket, files)
	opts.Config.Labels[docker.LabelConfigHash] = hash

	existing, err := dk.ListContainers(ctx, map[string]string{docker.LabelComponent: traefikComponent})
	if err != nil {
		return err
	}
	if len(existing) == 1 && existing[0].Labels[docker.LabelConfigHash] == hash {
		if existing[0].State == container.StateRunning {
			return nil
		}
		_, err := dk.ContainerStart(ctx, existing[0].ID, client.ContainerStartOptions{})
		return err
	}
	for _, ct := range existing {
		c.log.Info("removing outdated traefik container", "id", ct.ID[:12])
		if err := dk.RemoveContainer(ctx, ct.ID, 10*time.Second); err != nil {
			return err
		}
	}

	if err := dk.EnsureImage(ctx, cfg.Image); err != nil {
		return fmt.Errorf("pull %s: %w", cfg.Image, err)
	}
	if _, err := dk.Run(ctx, opts, files...); err != nil {
		return err
	}
	c.log.Info("traefik started", "server", sv.Name, "image", cfg.Image, "http", cfg.HTTPPort, "https", cfg.HTTPSPort)
	return nil
}

// socketPath is the Docker socket's path on a server's host, for containers
// that mount it (Traefik, builders).
func socketPath(sv store.Server, cfg config.Config) string {
	if sv.Kind == store.ServerSSH && sv.Socket != "" {
		return sv.Socket
	}
	return cfg.Traefik.DockerSocket
}

// managerUpstream is the URL Traefik uses to reach the manager. In a container
// the manager joins the proxy network itself, so its port needn't be published.
func (c *Core) managerUpstream(ctx context.Context, dk *docker.Client) (string, error) {
	_, port, err := net.SplitHostPort(c.cfg.Addr)
	if err != nil {
		return "", fmt.Errorf("KIPITINY_ADDR %q: %w", c.cfg.Addr, err)
	}
	if id := docker.SelfContainerID(); id != "" {
		if err := dk.ConnectNetwork(ctx, docker.ProxyNetwork, id, managerAlias); err != nil {
			return "", err
		}
		return "http://" + managerAlias + ":" + port, nil
	}
	return "http://" + managerHost + ":" + port, nil
}

// viaTunnel reports whether a server's public traffic comes through the
// Cloudflare tunnel, which only runs on the manager's own server.
func (c *Core) viaTunnel(serverID string) bool {
	return c.cfg.Tunnel.Token != "" && (serverID == "" || serverID == store.LocalServerID)
}

// acme reports whether routers on a server get Let's Encrypt certificates.
// Behind the tunnel Cloudflare terminates TLS, and the challenge can't reach
// Traefik anyway: Traefik's default certificate is enough.
func (c *Core) acme(serverID string) bool {
	return !c.viaTunnel(serverID)
}

// traefikSpec builds the Traefik container. A non-empty managerURL routes
// the manager's domain to it through a file provider. Behind the tunnel it
// publishes no ports and runs no ACME.
func (c *Core) traefikSpec(socket, managerURL string, tunnel bool) (client.ContainerCreateOptions, []docker.File) {
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
		// React to container start/stop quickly (default batches for 2s).
		"--providers.providersthrottleduration=200ms",
		// Backends are on a local bridge: a connect that takes over a second
		// means the container is gone, so fail fast and let retry pick another.
		"--serverstransport.forwardingtimeouts.dialtimeout=1s",
		"--providers.docker.exposedbydefault=false",
		"--providers.docker.network=" + docker.ProxyNetwork,
		// Ignore containers the manager does not own.
		"--providers.docker.constraints=Label(`" + docker.LabelManaged + "`,`true`)",
		"--entrypoints.web.address=:80",
		"--entrypoints.web.http.redirections.entrypoint.to=" + redirectTo,
		"--entrypoints.web.http.redirections.entrypoint.scheme=https",
		"--entrypoints.websecure.address=:443",
	}
	if !tunnel {
		args = append(args,
			"--certificatesresolvers."+certResolver+".acme.tlschallenge=true",
			"--certificatesresolvers."+certResolver+".acme.storage=/acme/acme.json",
		)
		if cfg.ACMEEmail != "" {
			args = append(args, "--certificatesresolvers."+certResolver+".acme.email="+cfg.ACMEEmail)
		}
	}
	var files []docker.File
	var extraHosts []string
	if managerURL != "" {
		args = append(args, "--providers.file.filename="+managerRouteFile)
		files = append(files, docker.File{Path: managerRouteFile, Content: managerRoute(c.cfg.Domain, managerURL, !tunnel)})
		if strings.Contains(managerURL, "//"+managerHost+":") {
			extraHosts = append(extraHosts, managerHost+":host-gateway")
		}
	}

	http, https := network.MustParsePort("80/tcp"), network.MustParsePort("443/tcp")
	ports := network.PortMap{
		http:  {{HostPort: cfg.HTTPPort}},
		https: {{HostPort: cfg.HTTPSPort}},
	}
	if tunnel {
		ports = nil // cloudflared reaches Traefik over the proxy network
	}
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
			PortBindings: ports,
			Mounts: []mount.Mount{
				{Type: mount.TypeBind, Source: socket, Target: "/var/run/docker.sock", ReadOnly: true},
				{Type: mount.TypeVolume, Source: traefikACMEVolume, Target: "/acme"},
			},
			ExtraHosts:    extraHosts,
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			LogConfig:     docker.DefaultLogConfig(),
		},
		NetworkingConfig: &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{docker.ProxyNetwork: {}},
		},
	}, files
}

// managerRoute is Traefik dynamic configuration serving domain from url. JSON
// is valid YAML, which the file provider reads.
func managerRoute(domain, url string, acme bool) []byte {
	tls := map[string]any{}
	if acme && !isLocalDomain(domain) {
		tls["certResolver"] = certResolver
	}
	b, _ := json.MarshalIndent(map[string]any{
		"http": map[string]any{
			"routers": map[string]any{managerAlias: map[string]any{
				"rule":        "Host(`" + domain + "`)",
				"entryPoints": []string{"websecure"},
				"service":     managerAlias,
				"tls":         tls,
			}},
			"services": map[string]any{managerAlias: map[string]any{
				"loadBalancer": map[string]any{"servers": []map[string]string{{"url": url}}},
			}},
		},
	}, "", "  ")
	return b
}

// traefikLabels routes HTTPS traffic for svc.Domain to svc.Port, with a Let's
// Encrypt certificate when acme is set.
func traefikLabels(svc store.Service, acme bool) map[string]string {
	name := "kipitiny-" + strings.ToLower(svc.ID)
	labels := map[string]string{
		"traefik.enable":                                "true",
		"traefik.docker.network":                        docker.ProxyNetwork,
		"traefik.http.routers." + name + ".rule":        "Host(`" + svc.Domain + "`)",
		"traefik.http.routers." + name + ".entrypoints": "websecure",
		"traefik.http.routers." + name + ".service":     name,
		// Connection failures (a replica stopping during a rollout, before
		// Traefik sees the event) are retried on another replica.
		"traefik.http.routers." + name + ".middlewares":                     name + "-retry@docker",
		"traefik.http.middlewares." + name + "-retry.retry.attempts":        "3",
		"traefik.http.middlewares." + name + "-retry.retry.initialinterval": "100ms",
		"traefik.http.services." + name + ".loadbalancer.server.port":       fmt.Sprint(svc.Port),
	}
	if !acme || isLocalDomain(svc.Domain) {
		// Let's Encrypt can't issue for these; Traefik's default cert is used.
		labels["traefik.http.routers."+name+".tls"] = "true"
	} else {
		labels["traefik.http.routers."+name+".tls.certresolver"] = certResolver
	}
	return labels
}

func specHash(parts ...any) string {
	b, _ := json.Marshal(parts)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

func isLocalDomain(d string) bool {
	return d == "localhost" || strings.HasSuffix(d, ".localhost")
}
