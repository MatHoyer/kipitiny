package network

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

const (
	TraefikName       = "kipitiny-traefik"
	TraefikComponent  = "traefik"
	traefikACMEVolume = "kipitiny-traefik-acme"
	CertResolverTLS   = "letsencrypt"

	// ManagerAlias names the manager on the proxy network when it runs in a
	// container; otherwise Traefik reaches it on the host.
	ManagerAlias = "kipitiny-manager"
	ManagerHost  = "host.docker.internal"
	// dynamicFile is Traefik's file provider: the manager's route and the
	// maintenance pages' service and middleware.
	dynamicFile = "/etc/traefik/kipitiny-manager.yml"
)

// EnsureTraefik makes sure exactly one Traefik container runs with the
// current configuration, recreating it when the configuration changed.
func (c *Network) EnsureTraefik(ctx context.Context, sv domain.Server) error {
	cfg := c.cfg.Traefik
	socket := socketPath(sv, *c.cfg)
	dk := c.mgr.DockerFor(sv.ID)
	o := TraefikOpts{Tunnel: c.TunnelToken(sv) != "", CFToken: c.cfToken(ctx)}
	if c.cfg.Domain != "" && sv.Kind == domain.ServerLocal {
		u, err := c.ManagerUpstream(ctx, dk)
		if err != nil {
			return err
		}
		o.ManagerURL = u
		o.ManagerResolver = c.CertResolver(ctx, sv.ID, c.cfg.Domain)
	}
	pages, err := c.mgr.PagesUpstream(ctx, sv, o.ManagerURL)
	if err != nil {
		return err
	}
	if pages != "" {
		token, err := c.mgr.ProviderToken(ctx, sv.ID)
		if err != nil {
			return err
		}
		o.Pages = &PagesOpts{Upstream: pages, ServerID: sv.ID, Token: token}
	}
	opts, files := c.TraefikSpec(socket, o)
	// Keep this formula stable: any change recreates Traefik on every server
	// at upgrade, a short outage for all apps. Tunnel mode changes Cmd.
	parts := []any{cfg.Image, opts.Config.Cmd, cfg.HTTPPort, cfg.HTTPSPort, socket, files}
	if o.CFToken != "" && !o.Tunnel {
		parts = append(parts, o.CFToken) // a new token must reach Traefik
	}
	hash := SpecHash(parts...)
	opts.Config.Labels[docker.LabelConfigHash] = hash

	existing, err := dk.ListSummaries(ctx, map[string]string{docker.LabelComponent: TraefikComponent})
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

	if err := dk.EnsureImage(ctx, cfg.Image, c.mgr.RegistryAuth(ctx, cfg.Image), ""); err != nil {
		return fmt.Errorf("pull %s: %w", cfg.Image, err)
	}
	if _, err := dk.RunMoby(ctx, opts, files...); err != nil {
		return err
	}
	c.log.Info("traefik started", "server", sv.Name, "image", cfg.Image, "http", cfg.HTTPPort, "https", cfg.HTTPSPort)
	return nil
}

// socketPath is the Docker socket's path on a server's host, for containers
// that mount it (Traefik, builders).
func socketPath(sv domain.Server, cfg config.Config) string {
	if sv.Kind == domain.ServerSSH && sv.Socket != "" {
		return sv.Socket
	}
	return cfg.Traefik.DockerSocket
}

// ManagerUpstream is the URL Traefik uses to reach the manager. In a container
// the manager joins the proxy network itself, so its port needn't be published.
func (c *Network) ManagerUpstream(ctx context.Context, dk *docker.Client) (string, error) {
	_, port, err := net.SplitHostPort(c.cfg.Addr)
	if err != nil {
		return "", fmt.Errorf("KIPITINY_ADDR %q: %w", c.cfg.Addr, err)
	}
	if id := docker.SelfContainerID(); id != "" {
		if err := dk.ConnectNetwork(ctx, docker.ProxyNetwork, id, ManagerAlias); err != nil {
			return "", err
		}
		return "http://" + ManagerAlias + ":" + port, nil
	}
	return "http://" + ManagerHost + ":" + port, nil
}

type TraefikOpts struct {
	// ManagerURL, when set, routes the manager's domain to it through a file
	// provider, with a certificate from ManagerResolver.
	ManagerURL      string
	ManagerResolver string
	// Tunnel: behind the Cloudflare tunnel, publish no ports and run no ACME.
	Tunnel bool
	// CFToken enables the Cloudflare DNS challenge (CertResolverDNS).
	CFToken string
	// Pages, when set, serves the manager's maintenance pages.
	Pages *PagesOpts
}

// PagesOpts are how a Traefik reaches the manager's maintenance pages and
// fetches its dynamic configuration (Core.TraefikConfig).
type PagesOpts struct {
	Upstream string
	ServerID string
	Token    string
}

// TraefikSpec builds the Traefik container.
func (c *Network) TraefikSpec(socket string, o TraefikOpts) (client.ContainerCreateOptions, []docker.File) {
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
		// Request counters per router, for sleep mode (sleep.go): on loopback,
		// read by an exec in the container.
		"--entrypoints.metrics.address=" + MetricsAddr,
		"--metrics.prometheus=true",
		"--metrics.prometheus.entrypoint=metrics",
		"--metrics.prometheus.addrouterslabels=true",
		"--metrics.prometheus.addentrypointslabels=false",
		"--metrics.prometheus.addserviceslabels=false",
	}
	if cfg.ReadTimeout != "" {
		// Unset, Traefik's 60s stays: adding the flag recreates Traefik.
		args = append(args, "--entrypoints.websecure.transport.respondingTimeouts.readTimeout="+cfg.ReadTimeout)
	}
	// Keep X-Forwarded-For from Cloudflare, so IP allowlists and rate
	// limits can see the client behind it (see Routing.BehindProxy).
	if tunnel {
		// Only cloudflared, on the proxy network, can reach Traefik.
		args = append(args, "--entrypoints.websecure.forwardedheaders.trustedips="+strings.Join(privateRanges, ","))
	} else if o.CFToken != "" {
		args = append(args, "--entrypoints.websecure.forwardedheaders.trustedips="+strings.Join(cloudflareRanges, ","))
	}
	if !tunnel {
		args = append(args,
			"--certificatesresolvers."+CertResolverTLS+".acme.tlschallenge=true",
			"--certificatesresolvers."+CertResolverTLS+".acme.storage=/acme/acme.json",
		)
		if cfg.ACMEEmail != "" {
			args = append(args, "--certificatesresolvers."+CertResolverTLS+".acme.email="+cfg.ACMEEmail)
		}
	}
	var env []string
	if !tunnel && o.CFToken != "" {
		args = append(args,
			"--certificatesresolvers."+CertResolverDNS+".acme.dnschallenge.provider=cloudflare",
			// Public resolvers see the challenge record sooner than a cache.
			"--certificatesresolvers."+CertResolverDNS+".acme.dnschallenge.resolvers=1.1.1.1:53,1.0.0.1:53",
			"--certificatesresolvers."+CertResolverDNS+".acme.storage=/acme/acme.json",
		)
		if cfg.ACMEEmail != "" {
			args = append(args, "--certificatesresolvers."+CertResolverDNS+".acme.email="+cfg.ACMEEmail)
		}
		env = append(env, "CF_DNS_API_TOKEN="+o.CFToken)
	}
	var files []docker.File
	var extraHosts []string
	dynamic := map[string]any{}
	if o.ManagerURL != "" {
		dynamic = managerRoute(c.cfg.Domain, o.ManagerURL, o.ManagerResolver)
	}
	if p := o.Pages; p != nil {
		// Sections (routers, services…) are merged: both define services.
		for k, v := range PagesFileConfig(p.Upstream) {
			if cur, ok := dynamic[k].(map[string]any); ok {
				maps.Copy(cur, v.(map[string]any))
			} else {
				dynamic[k] = v
			}
		}
		args = append(args,
			"--entrypoints.websecure.http.middlewares="+PagesDown+"@file",
			"--providers.http.endpoint="+p.Upstream+PagesPath+"traefik/"+p.ServerID,
			"--providers.http.headers.Authorization=Bearer "+p.Token,
		)
	}
	if len(dynamic) > 0 {
		// JSON is valid YAML, which the file provider reads.
		b, _ := json.MarshalIndent(map[string]any{"http": dynamic}, "", "  ")
		args = append(args, "--providers.file.filename="+dynamicFile)
		files = append(files, docker.File{Path: dynamicFile, Content: b})
	}
	for _, u := range []string{o.ManagerURL, pagesUpstream(o)} {
		if strings.Contains(u, "//"+ManagerHost+":") {
			extraHosts = append(extraHosts, ManagerHost+":host-gateway")
			break
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
		Name: TraefikName,
		Config: &container.Config{
			Image:        cfg.Image,
			Cmd:          args,
			Env:          env,
			ExposedPorts: network.PortSet{http: {}, https: {}},
			Labels: map[string]string{
				docker.LabelManaged:   "true",
				docker.LabelComponent: TraefikComponent,
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

func pagesUpstream(o TraefikOpts) string {
	if o.Pages == nil {
		return ""
	}
	return o.Pages.Upstream
}

// managerRoute is Traefik dynamic configuration (below http) serving domain
// from url.
func managerRoute(domain, url, resolver string) map[string]any {
	tls := map[string]any{}
	if resolver != "" {
		tls["certResolver"] = resolver
	}
	return map[string]any{
		"routers": map[string]any{ManagerAlias: map[string]any{
			"rule":        "Host(`" + domain + "`)",
			"entryPoints": []string{"websecure"},
			"service":     ManagerAlias,
			"tls":         tls,
		}},
		"services": map[string]any{ManagerAlias: map[string]any{
			"loadBalancer": map[string]any{"servers": []map[string]string{{"url": url}}},
		}},
	}
}

// Routing is how a public service's requests reach Traefik.
type Routing struct {
	// Resolver issues the certificate, "" for Traefik's default one.
	Resolver string
	// BehindProxy: requests come through Cloudflare (tunnel or proxied
	// record), which puts the client IP last in X-Forwarded-For.
	BehindProxy bool
	// DockerSocket is the socket's path on the server's host, for an app
	// that mounts it.
	DockerSocket string
}

func (c *Network) RouteFor(ctx context.Context, svc domain.Service) Routing {
	return Routing{
		Resolver: c.CertResolver(ctx, svc.ServerID, svc.Domain),
		// A domain with published ports (on any of its apps) is never proxied.
		BehindProxy:  !c.domainDirect(ctx, svc) && c.behindCloudflare(ctx, svc.ServerID, svc.Domain),
		DockerSocket: c.DockerSocketFor(ctx, svc),
	}
}

// DockerSocketFor is the Docker socket's path on svc's server, when svc
// mounts it.
func (c *Network) DockerSocketFor(ctx context.Context, svc domain.Service) string {
	if svc.DockerSocket == "" {
		return ""
	}
	sv, err := c.store.GetServer(ctx, svc.ServerID)
	if err != nil {
		c.log.Warn("server unavailable, using the default Docker socket", "server", svc.ServerID, "err", err)
	}
	return socketPath(sv, *c.cfg)
}

// HTTPRouted reports whether Traefik routes the service's domain: it has a
// container port. An app with published ports may have a domain only for
// DNS.
func HTTPRouted(svc domain.Service) bool {
	return svc.Domain != "" && svc.Port > 0
}

// TraefikLabels routes HTTPS traffic for svc.Domain to svc.Port through the
// service's middlewares; with routes, only their paths, each to its scheme
// and port.
//
// Traefik drops a router or middleware that containers define differently,
// and old and new replicas run side by side during a rollout, so the router
// and its middlewares are named after their configuration: a change adds a
// new router next to the old one instead of conflicting with it.
func TraefikLabels(svc domain.Service, rt Routing) map[string]string {
	name := "kipitiny-" + strings.ToLower(svc.ID)
	mws := MiddlewareLabels(svc.Middlewares, rt.BehindProxy)
	spec := []any{svc.Domain, rt.Resolver, mws}
	if len(svc.Routes) > 0 {
		spec = append(spec, svc.Routes) // without, the name of every app stays the same
	}
	router := name + "-" + SpecHash(spec...)[:8]
	labels := map[string]string{
		"traefik.enable":         "true",
		"traefik.docker.network": docker.ProxyNetwork,
		// Connection failures (a replica stopping during a rollout, before
		// Traefik sees the event) are retried on another replica.
		"traefik.http.middlewares." + name + "-retry.retry.attempts":        "3",
		"traefik.http.middlewares." + name + "-retry.retry.initialinterval": "100ms",
	}
	chain := make([]string, 0, len(mws)+1)
	for _, mw := range mws {
		mwName := router + "-" + mw.Suffix
		chain = append(chain, mwName+"@docker")
		for k, v := range mw.Labels {
			labels["traefik.http.middlewares."+mwName+"."+k] = v
		}
	}
	addRouter := func(r, rule, service string, chain []string) {
		labels["traefik.http.routers."+r+".rule"] = rule
		labels["traefik.http.routers."+r+".entrypoints"] = "websecure"
		labels["traefik.http.routers."+r+".service"] = service
		if len(chain) > 0 {
			labels["traefik.http.routers."+r+".middlewares"] = strings.Join(chain, ",")
		}
		if rt.Resolver == "" {
			labels["traefik.http.routers."+r+".tls"] = "true"
		} else {
			labels["traefik.http.routers."+r+".tls.certresolver"] = rt.Resolver
		}
	}
	retry := append(slices.Clone(chain), name+"-retry@docker")
	if len(svc.Routes) == 0 {
		labels["traefik.http.services."+name+".loadbalancer.server.port"] = fmt.Sprint(svc.Port)
		addRouter(router, "Host(`"+svc.Domain+"`)", name, retry)
		return labels
	}
	i := 0
	for _, r := range svc.Routes {
		service := RouteService(name, svc, r)
		labels["traefik.http.services."+service+".loadbalancer.server.port"] = fmt.Sprint(routePort(svc, r))
		c := retry
		if r.Scheme == "h2c" {
			labels["traefik.http.services."+service+".loadbalancer.server.scheme"] = "h2c"
			// The retry middleware holds a request's body until it ends,
			// which a bidirectional gRPC stream never does.
			c = chain
		}
		for _, p := range r.Paths {
			pr := fmt.Sprintf("%s-p%d", router, i)
			addRouter(pr, PathRule(svc.Domain, p), service, c)
			labels["traefik.http.routers."+pr+".priority"] = fmt.Sprint(PathPriority(p, true))
			i++
		}
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

func SpecHash(parts ...any) string {
	b, _ := json.Marshal(parts)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

func IsLocalDomain(d string) bool {
	return d == "localhost" || strings.HasSuffix(d, ".localhost")
}
