package core

import (
	"context"

	"github.com/kipit-labs/kipitiny/internal/docker"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

// networkPorts is what the network use cases reach of the rest of core
// (network.Manager).
type networkPorts struct{ c *Core }

func (p networkPorts) DockerFor(serverID string) *docker.Client { return p.c.dockerFor(serverID) }

func (p networkPorts) RegistryAuth(ctx context.Context, image string) string {
	return p.c.RegistryAuth(ctx, image)
}

func (p networkPorts) GoLoop(f func()) error { return p.c.goLoop(f) }

func (p networkPorts) Kick() { p.c.kick() }

func (p networkPorts) PagesUpstream(ctx context.Context, sv domain.Server, managerURL string) (string, error) {
	return p.c.PagesUpstream(ctx, sv, managerURL)
}

func (p networkPorts) ProviderToken(ctx context.Context, serverID string) (string, error) {
	return p.c.ProviderToken(ctx, serverID)
}

func (p networkPorts) AnalyticsDomain(ctx context.Context) string {
	if a := p.c.Analytics(ctx); a.Enabled {
		return a.Domain
	}
	return ""
}

func (p networkPorts) CheckSecretScheme(ctx context.Context, ref string) error {
	return p.c.CheckSecretScheme(ctx, ref)
}

func (p networkPorts) ResolveSecrets(ctx context.Context, refs []string) (map[string]string, error) {
	return p.c.ResolveSecrets(ctx, refs)
}
