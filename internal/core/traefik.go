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
	o := traefikOpts{Tunnel: c.tunnelToken(sv) != "", CFToken: c.cfToken(ctx)}
	if c.cfg.Domain != "" && sv.Kind == store.ServerLocal {
		u, err := c.managerUpstream(ctx, dk)
		if err != nil {
			return err
		}
		o.ManagerURL = u
		o.ManagerResolver = c.certResolver(ctx, sv.ID, c.cfg.Domain)
	}
	opts, files := c.traefikSpec(socket, o)
	// Keep this formula stable: any change recreates Traefik on every server
	// at upgrade, a short outage for all apps. Tunnel mode changes Cmd.
	parts := []any{cfg.Image, opts.Config.Cmd, cfg.HTTPPort, cfg.HTTPSPort, socket, files}
	if o.CFToken != "" && !o.Tunnel {
		parts = append(parts, o.CFToken) // a new token must reach Traefik
	}
	hash := specHash(parts...)
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

	if err := dk.EnsureImage(ctx, cfg.Image, c.registryAuth(ctx, cfg.Image)); err != nil {
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

type traefikOpts struct {
	// ManagerURL, when set, routes the manager's domain to it through a file
	// provider, with a certificate from ManagerResolver.
	ManagerURL      string
	ManagerResolver string
	// Tunnel: behind the Cloudflare tunnel, publish no ports and run no ACME.
	Tunnel bool
	// CFToken enables the Cloudflare DNS challenge (certResolverDNS).
	CFToken string
}

// traefikSpec builds the Traefik container.
func (c *Core) traefikSpec(socket string, o traefikOpts) (client.ContainerCreateOptions, []docker.File) {
	cfg := c.cfg.Traefik
	tunnel := o.Tunnel

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
	// Keep X-Forwarded-For from Cloudflare, so IP allowlists and rate
	// limits can see the client behind it (see route.behindProxy).
	if tunnel {
		// Only cloudflared, on the proxy network, can reach Traefik.
		args = append(args, "--entrypoints.websecure.forwardedheaders.trustedips="+strings.Join(privateRanges, ","))
	} else if o.CFToken != "" {
		args = append(args, "--entrypoints.websecure.forwardedheaders.trustedips="+strings.Join(cloudflareRanges, ","))
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
	var env []string
	if !tunnel && o.CFToken != "" {
		args = append(args,
			"--certificatesresolvers."+certResolverDNS+".acme.dnschallenge.provider=cloudflare",
			// Public resolvers see the challenge record sooner than a cache.
			"--certificatesresolvers."+certResolverDNS+".acme.dnschallenge.resolvers=1.1.1.1:53,1.0.0.1:53",
			"--certificatesresolvers."+certResolverDNS+".acme.storage=/acme/acme.json",
		)
		if cfg.ACMEEmail != "" {
			args = append(args, "--certificatesresolvers."+certResolverDNS+".acme.email="+cfg.ACMEEmail)
		}
		env = append(env, "CF_DNS_API_TOKEN="+o.CFToken)
	}
	var files []docker.File
	var extraHosts []string
	if o.ManagerURL != "" {
		args = append(args, "--providers.file.filename="+managerRouteFile)
		files = append(files, docker.File{Path: managerRouteFile, Content: managerRoute(c.cfg.Domain, o.ManagerURL, o.ManagerResolver)})
		if strings.Contains(o.ManagerURL, "//"+managerHost+":") {
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
			Env:          env,
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
func managerRoute(domain, url, resolver string) []byte {
	tls := map[string]any{}
	if resolver != "" {
		tls["certResolver"] = resolver
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

// route is how a public service's requests reach Traefik.
type route struct {
	// resolver issues the certificate, "" for Traefik's default one.
	resolver string
	// behindProxy: requests come through Cloudflare (tunnel or proxied
	// record), which puts the client IP last in X-Forwarded-For.
	behindProxy bool
	// dockerSocket is the socket's path on the server's host, for an app
	// that mounts it.
	dockerSocket string
}

func (c *Core) routeFor(ctx context.Context, svc store.Service) route {
	return route{
		resolver: c.certResolver(ctx, svc.ServerID, svc.Domain),
		// The domain of an app with published ports is never proxied.
		behindProxy:  len(svc.PublishedPorts) == 0 && c.behindCloudflare(ctx, svc.ServerID, svc.Domain),
		dockerSocket: c.dockerSocketFor(ctx, svc),
	}
}

// dockerSocketFor is the Docker socket's path on svc's server, when svc
// mounts it.
func (c *Core) dockerSocketFor(ctx context.Context, svc store.Service) string {
	if svc.DockerSocket == "" {
		return ""
	}
	sv, err := c.store.GetServer(ctx, svc.ServerID)
	if err != nil {
		c.log.Warn("server unavailable, using the default Docker socket", "server", svc.ServerID, "err", err)
	}
	return socketPath(sv, c.cfg)
}

// httpRouted reports whether Traefik routes the service's domain: it has a
// container port. An app with published ports may have a domain only for
// DNS.
func httpRouted(svc store.Service) bool {
	return svc.Domain != "" && svc.Port > 0
}

// traefikLabels routes HTTPS traffic for svc.Domain to svc.Port through the
// service's middlewares.
//
// Traefik drops a router or middleware that containers define differently,
// and old and new replicas run side by side during a rollout, so the router
// and its middlewares are named after their configuration: a change adds a
// new router next to the old one instead of conflicting with it.
func traefikLabels(svc store.Service, rt route) map[string]string {
	name := "kipitiny-" + strings.ToLower(svc.ID)
	mws := middlewareLabels(svc.Middlewares, rt.behindProxy)
	router := name + "-" + specHash(svc.Domain, rt.resolver, mws)[:8]
	labels := map[string]string{
		"traefik.enable":                                  "true",
		"traefik.docker.network":                          docker.ProxyNetwork,
		"traefik.http.routers." + router + ".rule":        "Host(`" + svc.Domain + "`)",
		"traefik.http.routers." + router + ".entrypoints": "websecure",
		"traefik.http.routers." + router + ".service":     name,
		// Connection failures (a replica stopping during a rollout, before
		// Traefik sees the event) are retried on another replica.
		"traefik.http.middlewares." + name + "-retry.retry.attempts":        "3",
		"traefik.http.middlewares." + name + "-retry.retry.initialinterval": "100ms",
		"traefik.http.services." + name + ".loadbalancer.server.port":       fmt.Sprint(svc.Port),
	}
	chain := make([]string, 0, len(mws)+1)
	for _, mw := range mws {
		mwName := router + "-" + mw.Suffix
		chain = append(chain, mwName+"@docker")
		for k, v := range mw.Labels {
			labels["traefik.http.middlewares."+mwName+"."+k] = v
		}
	}
	labels["traefik.http.routers."+router+".middlewares"] = strings.Join(append(chain, name+"-retry@docker"), ",")
	if rt.resolver == "" {
		labels["traefik.http.routers."+router+".tls"] = "true"
	} else {
		labels["traefik.http.routers."+router+".tls.certresolver"] = rt.resolver
	}
	return labels
}

// privateRanges are the networks Docker assigns container addresses from.
var privateRanges = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fd00::/8"}

// cloudflareRanges are Cloudflare's edge addresses
// (https://www.cloudflare.com/ips/).
var cloudflareRanges = []string{
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22", "141.101.64.0/18",
	"108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20", "197.234.240.0/22", "198.41.128.0/17",
	"162.158.0.0/15", "104.16.0.0/13", "104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
	"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32", "2405:8100::/32",
	"2a06:98c0::/29", "2c0f:f248::/32",
}

func specHash(parts ...any) string {
	b, _ := json.Marshal(parts)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

func isLocalDomain(d string) bool {
	return d == "localhost" || strings.HasSuffix(d, ".localhost")
}
