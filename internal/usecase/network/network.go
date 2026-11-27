// Package network is how apps are reached: domains and their DNS records
// (Cloudflare), Traefik and its routes and middlewares, published ports,
// Cloudflare tunnels, and which proxies the manager trusts.
package network

import (
	"context"
	"log/slog"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

// Manager is what network uses from the rest of the manager.
type Manager interface {
	// DockerFor is the Docker client of a server.
	DockerFor(serverID string) domain.ContainerRuntime
	// RegistryAuth is the credential to pull image with, "" for none.
	RegistryAuth(ctx context.Context, image string) string
	// GoLoop runs f in the background until the manager stops.
	GoLoop(f func()) error
	// Kick asks the reconciler for a pass (Traefik and cloudflared follow
	// the settings it reads).
	Kick()
	// PagesUpstream is where a server's Traefik reaches the maintenance
	// pages, "" for none; ProviderToken authenticates its configuration
	// requests.
	PagesUpstream(ctx context.Context, sv domain.Server, managerURL string) (string, error)
	ProviderToken(ctx context.Context, serverID string) (string, error)
	// AnalyticsDomain is the analytics server's domain, "" when it is off.
	AnalyticsDomain(ctx context.Context) string
	// CheckSecretScheme and ResolveSecrets read password managers, for
	// basic auth passwords given as references.
	CheckSecretScheme(ctx context.Context, ref string) error
	ResolveSecrets(ctx context.Context, refs []string) (map[string]string, error)
}

type Network struct {
	// cfg is the manager's configuration, shared with core.
	cfg   *config.Config
	store domain.Store
	mgr   Manager
	log   *slog.Logger
	// bg ends when the manager stops: the DNS sync loop runs under it.
	bg context.Context

	dns     dnsState
	dnsKick chan struct{}
	proxy   proxyState
}

// New builds the network use cases; bg is the manager's background context.
func New(bg context.Context, cfg *config.Config, store domain.Store, mgr Manager, log *slog.Logger) *Network {
	return &Network{cfg: cfg, store: store, mgr: mgr, log: log, bg: bg, dnsKick: make(chan struct{}, 1)}
}
